package signer

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/decred/dcrd/hdkeychain/v3"
	opcrypto "github.com/ethereum-optimism/optimism/op-service/crypto"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/usbwallet"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
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

type ecdsaSigner struct {
	*ecdsa.PrivateKey
}

func (s *ecdsaSigner) Address() common.Address {
	return crypto.PubkeyToAddress(s.PublicKey)
}

func (s *ecdsaSigner) SignerFn(chainID *big.Int) bind.SignerFn {
	return opcrypto.PrivateKeySignerFn(s.PrivateKey, chainID)
}

func (s *ecdsaSigner) SignData(data []byte) ([]byte, error) {
	sig, err := crypto.Sign(crypto.Keccak256(data), s.PrivateKey)
	if err != nil {
		return nil, err
	}
	sig[crypto.RecoveryIDOffset] += 27
	return sig, err
}

type walletSigner struct {
	wallet  accounts.Wallet
	account accounts.Account
}

func (s *walletSigner) Address() common.Address {
	return s.account.Address
}

func (s *walletSigner) SignerFn(chainID *big.Int) bind.SignerFn {
	return func(address common.Address, tx *types.Transaction) (*types.Transaction, error) {
		return s.wallet.SignTx(s.account, tx, chainID)
	}
}

func (s *walletSigner) SignData(data []byte) ([]byte, error) {
	return s.wallet.SignData(s.account, accounts.MimetypeTypedData, data)
}

func derivePrivateKey(mnemonic string, path accounts.DerivationPath) (*ecdsa.PrivateKey, error) {
	// Parse the seed string into the master BIP32 key.
	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return nil, err
	}

	privKey, err := hdkeychain.NewMaster(seed, fakeNetworkParams{})
	if err != nil {
		return nil, err
	}

	for _, child := range path {
		privKey, err = privKey.Child(child)
		if err != nil {
			return nil, err
		}
	}

	rawPrivKey, err := privKey.SerializedPrivKey()
	if err != nil {
		return nil, err
	}

	return crypto.ToECDSA(rawPrivKey)
}

type fakeNetworkParams struct{}

func (f fakeNetworkParams) HDPrivKeyVersion() [4]byte {
	return [4]byte{}
}

func (f fakeNetworkParams) HDPubKeyVersion() [4]byte {
	return [4]byte{}
}
