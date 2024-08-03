package withdraw

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum-optimism/optimism/op-node/bindings"
	bindingspreview "github.com/ethereum-optimism/optimism/op-node/bindings/preview"
	"github.com/ethereum-optimism/optimism/op-node/withdrawals"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/ethclient/gethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type FPWithdrawer struct {
	Ctx      context.Context
	L1Client *ethclient.Client
	L2Client *rpc.Client
	L2TxHash common.Hash
	Portal   *bindingspreview.OptimismPortal2
	Factory  *bindings.DisputeGameFactory
	Opts     *bind.TransactOpts
}

func (w *FPWithdrawer) GetWithdrawalHash() (common.Hash, error) {
	l2 := ethclient.NewClient(w.L2Client)
	receipt, err := l2.TransactionReceipt(w.Ctx, w.L2TxHash)
	if err != nil {
		return common.HexToHash(""), err
	}

	ev, err := withdrawals.ParseMessagePassed(receipt)
	if err != nil {
		return common.HexToHash(""), err
	}

	hash, err := withdrawals.WithdrawalHash(ev)
	if err != nil {
		return common.HexToHash(""), err
	}

	return hash, nil
}

func (w *FPWithdrawer) IsProofFinalized() (bool, error) {
	return w.Portal.FinalizedWithdrawals(&bind.CallOpts{}, w.L2TxHash)
}

func (w *FPWithdrawer) GetProvenWithdrawal() (struct {
	DisputeGameProxy common.Address
	Timestamp        uint64
}, error) {
	// the proven withdrawal structure now contains an additional mapping, as withdrawal proofs are now stored per submitter address
	empty := *new(struct {
		DisputeGameProxy common.Address
		Timestamp        uint64
	})

	hash, err := w.GetWithdrawalHash()
	if err != nil {
		return empty, err
	}

	return w.Portal.ProvenWithdrawals(&bind.CallOpts{}, hash, w.Opts.From)
}

func (w *FPWithdrawer) ProveWithdrawal() error {
	l2 := ethclient.NewClient(w.L2Client)
	l2g := gethclient.New(w.L2Client)

	params, err := withdrawals.ProveWithdrawalParametersFaultProofs(w.Ctx, l2g, l2, l2, w.L2TxHash, &w.Factory.DisputeGameFactoryCaller, &w.Portal.OptimismPortal2Caller)
	if err != nil {
		return err
	}

	// create the proof
	tx, err := w.Portal.ProveWithdrawalTransaction(
		w.Opts,
		bindingspreview.TypesWithdrawalTransaction{
			Nonce:    params.Nonce,
			Sender:   params.Sender,
			Target:   params.Target,
			Value:    params.Value,
			GasLimit: params.GasLimit,
			Data:     params.Data,
		},
		params.L2OutputIndex, // this is overloaded and is the DisputeGame index in this context
		bindingspreview.TypesOutputRootProof{
			Version:                  params.OutputRootProof.Version,
			StateRoot:                params.OutputRootProof.StateRoot,
			MessagePasserStorageRoot: params.OutputRootProof.MessagePasserStorageRoot,
			LatestBlockhash:          params.OutputRootProof.LatestBlockhash,
		},
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

func (w *FPWithdrawer) FinalizeWithdrawal() error {
	// get the withdrawal hash
	hash, err := w.GetWithdrawalHash()
	if err != nil {
		return err
	}

	// check if the withdrawal can be finalized using the calculated withdrawal hash
	err = w.Portal.CheckWithdrawal(&bind.CallOpts{}, hash, w.Opts.From)
	if err != nil {
		return err
	}

	// get the WithdrawalTransaction info needed to finalize the withdrawal
	l2 := ethclient.NewClient(w.L2Client)
	l2g := gethclient.New(w.L2Client)

	// we only use info from this call that isn't block-specific, so it's safe to call this again
	params, err := withdrawals.ProveWithdrawalParametersFaultProofs(w.Ctx, l2g, l2, l2, w.L2TxHash, &w.Factory.DisputeGameFactoryCaller, &w.Portal.OptimismPortal2Caller)
	if err != nil {
		return err
	}

	// finalize the withdrawal
	tx, err := w.Portal.FinalizeWithdrawalTransaction(
		w.Opts,
		bindingspreview.TypesWithdrawalTransaction{
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
