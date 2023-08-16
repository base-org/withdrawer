package signer

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/usbwallet"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Signer interface {
	Address() common.Address
	SignerFn(chainID *big.Int) bind.SignerFn
	SignData([]byte) ([]byte, error)
}

func CreateSigner(privateKey, mnemonic, hdPath string) (Signer, error) {
	if privateKey != "" {
		key, err := crypto.HexToECDSA(privateKey)
		if err != nil {
			return nil, fmt.Errorf("error parsing private key: %w", err)
		}
		return &ecdsaSigner{key}, nil
	}

	path, err := accounts.ParseDerivationPath(hdPath)
	if err != nil {
		return nil, err
	}

	if mnemonic != "" {
		key, err := derivePrivateKey(mnemonic, path)
		if err != nil {
			return nil, fmt.Errorf("error deriving key from mnemonic: %w", err)
		}
		return &ecdsaSigner{key}, nil
	}

	// assume using a ledger
	ledgerHub, err := usbwallet.NewLedgerHub()
	if err != nil {
		return nil, fmt.Errorf("error starting ledger: %w", err)
	}
	wallets := ledgerHub.Wallets()
	if len(wallets) == 0 {
		return nil, fmt.Errorf("no ledgers found, please connect your ledger")
	} else if len(wallets) > 1 {
		return nil, fmt.Errorf("multiple ledgers found, please use one ledger at a time")
	}
	wallet := wallets[0]
	if err := wallet.Open(""); err != nil {
		return nil, fmt.Errorf("error opening ledger: %w", err)
	}
	account, err := wallet.Derive(path, true)
	if err != nil {
		return nil, fmt.Errorf("error deriving ledger account (have you unlocked?): %w", err)
	}
	return &walletSigner{
		wallet:  wallet,
		account: account,
	}, nil
}
