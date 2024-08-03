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

func getWithdrawalHash(ctx context.Context, l2c *rpc.Client, l2TxHash common.Hash) (common.Hash, error) {
	l2 := ethclient.NewClient(l2c)
	receipt, err := l2.TransactionReceipt(ctx, l2TxHash)
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

func FpProofFinalized(ctx context.Context, portal *bindingspreview.OptimismPortal2, l2TxHash common.Hash) (bool, error) {
	return portal.FinalizedWithdrawals(&bind.CallOpts{}, l2TxHash)
}

func FpProvenWithdrawal(ctx context.Context, l2c *rpc.Client, portal *bindingspreview.OptimismPortal2, l2TxHash common.Hash, submitter common.Address) (struct {
	DisputeGameProxy common.Address
	Timestamp        uint64
}, error) {
	// the proven withdrawal structure now contains an additional mapping, as withdrawal proofs are now stored per submitter address
	empty := *new(struct {
		DisputeGameProxy common.Address
		Timestamp        uint64
	})

	hash, err := getWithdrawalHash(ctx, l2c, l2TxHash)
	if err != nil {
		return empty, err
	}

	return portal.ProvenWithdrawals(&bind.CallOpts{}, hash, submitter)
}

func FpProveWithdrawal(ctx context.Context, l1 *ethclient.Client, l2c *rpc.Client, disputeGameFactory *bindings.DisputeGameFactory, portal *bindingspreview.OptimismPortal2, l2TxHash common.Hash, opts *bind.TransactOpts) error {
	l2 := ethclient.NewClient(l2c)
	l2g := gethclient.New(l2c)

	params, err := withdrawals.ProveWithdrawalParametersFaultProofs(ctx, l2g, l2, l2, l2TxHash, &disputeGameFactory.DisputeGameFactoryCaller, &portal.OptimismPortal2Caller)
	if err != nil {
		return err
	}

	// create the proof
	tx, err := portal.ProveWithdrawalTransaction(
		opts,
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

	fmt.Printf("Proved withdrawal for %s: %s\n", l2TxHash.String(), tx.Hash().String())

	// Wait 5 mins max for confirmation
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return WaitForConfirmation(ctxWithTimeout, l1, tx.Hash())
}

func FpFinalizeWithdrawal(ctx context.Context, l1 *ethclient.Client, l2c *rpc.Client, disputeGameFactory *bindings.DisputeGameFactory, portal *bindingspreview.OptimismPortal2, l2TxHash common.Hash, opts *bind.TransactOpts) error {
	// get the withdrawal hash
	hash, err := getWithdrawalHash(ctx, l2c, l2TxHash)
	if err != nil {
		return err
	}

	// check if the withdrawal can be finalized using the calculated withdrawal hash
	err = portal.CheckWithdrawal(&bind.CallOpts{}, hash, opts.From)
	if err != nil {
		return err
	}

	// get the WithdrawalTransaction info needed to finalize the withdrawal
	l2 := ethclient.NewClient(l2c)
	l2g := gethclient.New(l2c)

	// we only use info from this call that isn't block-specific, so it's safe to call this again
	params, err := withdrawals.ProveWithdrawalParametersFaultProofs(ctx, l2g, l2, l2, l2TxHash, &disputeGameFactory.DisputeGameFactoryCaller, &portal.OptimismPortal2Caller)
	if err != nil {
		return err
	}

	// finalize the withdrawal
	tx, err := portal.FinalizeWithdrawalTransaction(
		opts,
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

	fmt.Printf("Completed withdrawal for %s: %s\n", l2TxHash.String(), tx.Hash().String())

	// Wait 5 mins max for confirmation
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return WaitForConfirmation(ctxWithTimeout, l1, tx.Hash())
}
