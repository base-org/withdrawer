package withdraw

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum-optimism/optimism/op-node/bindings"
	"github.com/ethereum-optimism/optimism/op-node/withdrawals"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type Withdrawer struct {
	Ctx      context.Context
	L1Client *ethclient.Client
	L2Client *rpc.Client
	L2TxHash common.Hash
	Portal   *bindings.OptimismPortal
	Oracle   *bindings.L2OutputOracle
	Opts     *bind.TransactOpts
}

func (w *Withdrawer) CheckIfProvable() error {
	// check to make sure it is possible to prove the provided withdrawal
	submissionInterval, err := w.Oracle.SUBMISSIONINTERVAL(&bind.CallOpts{})
	if err != nil {
		return fmt.Errorf("error querying output proposal submission interval: %s", err)
	}

	l2BlockTime, err := w.Oracle.L2BLOCKTIME(&bind.CallOpts{})
	if err != nil {
		return fmt.Errorf("error querying output proposal L2 block time: %s", err)
	}

	l2OutputBlock, err := w.Oracle.LatestBlockNumber(&bind.CallOpts{})
	if err != nil {
		return fmt.Errorf("error querying latest proposed block: %s", err)
	}

	l2WithdrawalBlock, err := TxBlock(w.Ctx, w.L2Client, w.L2TxHash)
	if err != nil {
		return fmt.Errorf("error querying withdrawal tx block: %s", err)
	}

	if l2OutputBlock.Uint64() < l2WithdrawalBlock.Uint64() {
		return fmt.Errorf("the latest L2 output is %d and is not past L2 block %d that includes the withdrawal, no withdrawal can be proved yet.\nPlease wait for the next proposal submission, which happens every %v",
			l2OutputBlock.Uint64(), l2WithdrawalBlock.Uint64(), time.Duration(submissionInterval.Int64()*l2BlockTime.Int64())*time.Second)
	}
	return nil
}

func (w *Withdrawer) GetProvenWithdrawal() (struct {
	OutputRoot    [32]byte
	Timestamp     *big.Int
	L2OutputIndex *big.Int
}, error) {
	empty := *new(struct {
		OutputRoot    [32]byte
		Timestamp     *big.Int
		L2OutputIndex *big.Int
	})

	l2 := ethclient.NewClient(w.L2Client)
	receipt, err := l2.TransactionReceipt(w.Ctx, w.L2TxHash)
	if err != nil {
		return empty, err
	}

	ev, err := withdrawals.ParseMessagePassed(receipt)
	if err != nil {
		return empty, err
	}

	hash, err := withdrawals.WithdrawalHash(ev)
	if err != nil {
		return empty, err
	}

	return w.Portal.ProvenWithdrawals(&bind.CallOpts{}, hash)
}

func (w *Withdrawer) ProveWithdrawal() error {
	l2 := ethclient.NewClient(w.L2Client)
	l2g := gethclient.New(w.L2Client)

	l2OutputBlock, err := w.Oracle.LatestBlockNumber(&bind.CallOpts{})
	if err != nil {
		return err
	}

	// We generate a proof for the latest L2 output, which shouldn't require archive-node data if it's recent enough.
	header, err := l2.HeaderByNumber(w.Ctx, l2OutputBlock)
	if err != nil {
		return err
	}
	params, err := withdrawals.ProveWithdrawalParameters(w.Ctx, l2g, l2, l2, w.L2TxHash, header, &w.Oracle.L2OutputOracleCaller)
	if err != nil {
		return err
	}

	// Create the prove tx
	tx, err := w.Portal.ProveWithdrawalTransaction(
		w.Opts,
		bindings.TypesWithdrawalTransaction{
			Nonce:    params.Nonce,
			Sender:   params.Sender,
			Target:   params.Target,
			Value:    params.Value,
			GasLimit: params.GasLimit,
			Data:     params.Data,
		},
		params.L2OutputIndex,
		params.OutputRootProof,
		params.WithdrawalProof,
	)
	if err != nil {
		return err
	}

	fmt.Printf("Proved withdrawal for %s: %s\n", w.L2TxHash.String(), tx.Hash().String())

	// Wait 5 mins max for confirmation
	ctxWithTimeout, cancel := context.WithTimeout(w.Ctx, 5*time.Minute)
	defer cancel()
	return WaitForConfirmation(ctxWithTimeout, w.L1Client, tx.Hash())
}

func (w *Withdrawer) IsProofFinalized() (bool, error) {
	return w.Portal.FinalizedWithdrawals(&bind.CallOpts{}, w.L2TxHash)
}

func (w *Withdrawer) FinalizeWithdrawal() error {
	l2 := ethclient.NewClient(w.L2Client)
	l2g := gethclient.New(w.L2Client)

	// Figure out when our withdrawal was included
	receipt, err := l2.TransactionReceipt(w.Ctx, w.L2TxHash)
	if err != nil {
		return fmt.Errorf("cannot get receipt for withdrawal tx %s: %v", w.L2TxHash, err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return errors.New("unsuccessful withdrawal receipt status")
	}

	l2WithdrawalBlock, err := l2.HeaderByNumber(w.Ctx, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("error getting header by number for block %s: %v", receipt.BlockNumber, err)
	}

	// Figure out what the Output oracle on L1 has seen so far
	l2OutputBlockNr, err := w.Oracle.LatestBlockNumber(&bind.CallOpts{})
	if err != nil {
		return err
	}

	l2OutputBlock, err := l2.HeaderByNumber(w.Ctx, l2OutputBlockNr)
	if err != nil {
		return fmt.Errorf("error getting header by number for latest block %s: %v", l2OutputBlockNr, err)
	}

	// Check if the L2 output is even old enough to include the withdrawal
	if l2OutputBlock.Number.Uint64() < l2WithdrawalBlock.Number.Uint64() {
		fmt.Printf("the latest L2 output is %d and is not past L2 block %d that includes the withdrawal yet, no withdrawal can be completed yet", l2OutputBlock.Number.Uint64(), l2WithdrawalBlock.Number.Uint64())
		return nil
	}

	l1Head, err := w.L1Client.HeaderByNumber(w.Ctx, nil)
	if err != nil {
		return err
	}

	// Check if the withdrawal may be completed yet
	finalizationPeriod, err := w.Oracle.FINALIZATIONPERIODSECONDS(&bind.CallOpts{})
	if err != nil {
		return err
	}

	if l2WithdrawalBlock.Time+finalizationPeriod.Uint64() >= l1Head.Time {
		fmt.Printf("withdrawal tx %s was included in L2 block %d (time %d) but L1 only knows of L2 proposal %d (time %d) at head %d (time %d) which has not reached output confirmation yet (period is %d)",
			w.L2TxHash, l2WithdrawalBlock.Number.Uint64(), l2WithdrawalBlock.Time, l2OutputBlock.Number.Uint64(), l2OutputBlock.Time, l1Head.Number.Uint64(), l1Head.Time, finalizationPeriod.Uint64())
		return nil
	}

	// We generate a proof for the latest L2 output, which shouldn't require archive-node data if it's recent enough.
	// Note that for the `FinalizeWithdrawalTransaction` function, this proof isn't needed. We simply use some of the
	// params for the `WithdrawalTransaction` type generated in the bindings.
	header, err := l2.HeaderByNumber(w.Ctx, l2OutputBlockNr)
	if err != nil {
		return err
	}

	params, err := withdrawals.ProveWithdrawalParameters(w.Ctx, l2g, l2, l2, w.L2TxHash, header, &w.Oracle.L2OutputOracleCaller)
	if err != nil {
		return err
	}

	// Create the withdrawal tx
	tx, err := w.Portal.FinalizeWithdrawalTransaction(
		w.Opts,
		bindings.TypesWithdrawalTransaction{
			Nonce:    params.Nonce,
			Sender:   params.Sender,
			Target:   params.Target,
			Value:    params.Value,
			GasLimit: params.GasLimit,
			Data:     params.Data,
		},
	)
	if err != nil {
		return err
	}

	fmt.Printf("Completed withdrawal for %s: %s\n", w.L2TxHash.String(), tx.Hash().String())

	// Wait 5 mins max for confirmation
	ctxWithTimeout, cancel := context.WithTimeout(w.Ctx, 5*time.Minute)
	defer cancel()
	return WaitForConfirmation(ctxWithTimeout, w.L1Client, tx.Hash())
}
