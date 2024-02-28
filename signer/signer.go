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

// Signer defines the interface for interacting with different types of signers.
type Signer interface {
	Address() common.Address        // Address returns the Ethereum address associated with the signer.
	SignerFn(chainID *big.Int) bind.SignerFn // SignerFn returns a signer function used for transaction signing.
	SignData([]byte) ([]byte, error) // SignData signs the given data using the signer's private key.
}

// CreateSigner creates a signer based on the provided private key, mnemonic, or hardware wallet.
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
		key, err := derivePrivateKeyFromMnemonic(mnemonic, path)
		if err != nil {
			return nil, fmt.Errorf("error deriving key from mnemonic: %w", err)
		}
		return &ecdsaSigner{key}, nil
	}

	// Assume using a hardware wallet (e.g., Ledger)
	ledgerHub, err := usbwallet.NewLedgerHub()
	if err != nil {
		return nil, fmt.Errorf("error starting Ledger: %w", err)
	}
	wallets := ledgerHub.Wallets()
	if len(wallets) == 0 {
		return nil, fmt.Errorf("no Ledger device found, please connect your Ledger")
	} else if len(wallets) > 1 {
		return nil, fmt.Errorf("multiple Ledger devices found, please use only one at a time")
	}
	wallet := wallets[0]
	if err := wallet.Open(""); err != nil {
		return nil, fmt.Errorf("error opening Ledger: %w", err)
	}
	account, err := wallet.Derive(path, true)
	if err != nil {
		return nil, fmt.Errorf("error deriving Ledger account (have you unlocked?): %w", err)
	}
	return &walletSigner{
		wallet:  wallet,
		account: account,
	}, nil
}
