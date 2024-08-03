package withdraw

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

func TxBlock(ctx context.Context, l2c *rpc.Client, l2TxHash common.Hash) (*big.Int, error) {
	l2 := ethclient.NewClient(l2c)
	// Figure out when our withdrawal was included
	receipt, err := l2.TransactionReceipt(ctx, l2TxHash)
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, errors.New("unsuccessful withdrawal receipt status")
	}
	return receipt.BlockNumber, nil
}

func WaitForConfirmation(ctx context.Context, client *ethclient.Client, tx common.Hash) error {
	for {
		receipt, err := client.TransactionReceipt(ctx, tx)
		if err == ethereum.NotFound {
			fmt.Printf("waiting for tx confirmation\n")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		} else if err != nil {
			return err
		} else if receipt.Status != types.ReceiptStatusSuccessful {
			return errors.New("unsuccessful withdrawal receipt status")
		} else {
			break
		}
	}
	fmt.Printf("%s confirmed\n", tx.String())
	return nil
}
