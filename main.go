package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/base-org/withdrawer/signer"
	"github.com/base-org/withdrawer/withdraw"
	"github.com/ethereum-optimism/optimism/op-bindings/bindings"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type network struct {
	l2RPC         string
	portalAddress string
	l2OOAddress   string
}

var networks = map[string]network{
	"base-mainnet": {
		l2RPC:         "https://mainnet.base.org",
		portalAddress: "0x49048044D57e1C92A77f79988d21Fa8fAF74E97e",
		l2OOAddress:   "0x56315b90c40730925ec5485cf004d835058518A0",
	},
	"base-goerli": {
		l2RPC:         "https://goerli.base.org",
		portalAddress: "0xe93c8cD0D409341205A592f8c4Ac1A5fe5585cfA",
		l2OOAddress:   "0x2A35891ff30313CcFa6CE88dcf3858bb075A2298",
	},
	"op-mainnet": {
		l2RPC:         "https://mainnet.optimism.io",
		portalAddress: "0xbEb5Fc579115071764c7423A4f12eDde41f106Ed",
		l2OOAddress:   "0xdfe97868233d1aa22e815a266982f2cf17685a27",
	},
	"op-goerli": {
		l2RPC:         "https://goerli.optimism.io",
		portalAddress: "0x5b47E1A08Ea6d985D6649300584e6722Ec4B1383",
		l2OOAddress:   "0xE6Dfba0953616Bacab0c9A8ecb3a9BBa77FC15c0",
	},
}

func main() {
	var networkKeys []string
	for n := range networks {
		networkKeys = append(networkKeys, n)
	}

	var rpcFlag string
	var networkFlag string
	var l2RpcFlag string
	var portalAddress string
	var l2OOAddress string
	var withdrawalFlag string
	var privateKey string
	var ledger bool
	var mnemonic string
	var hdPath string
	flag.StringVar(&rpcFlag, "rpc", "", "Ethereum L1 RPC url")
	flag.StringVar(&networkFlag, "network", "base-mainnet", fmt.Sprintf("op-stack network to withdraw.go from (one of: %s)", strings.Join(networkKeys, ", ")))
	flag.StringVar(&l2RpcFlag, "l2-rpc", "", "Custom network L2 RPC url")
	flag.StringVar(&portalAddress, "portal-address", "", "Custom network OptimismPortal address")
	flag.StringVar(&l2OOAddress, "l2oo-address", "", "Custom network L2OutputOracle address")
	flag.StringVar(&withdrawalFlag, "withdrawal", "", "TX hash of the L2 withdrawal transaction")
	flag.StringVar(&privateKey, "private-key", "", "Private key to use for signing transactions")
	flag.BoolVar(&ledger, "ledger", false, "Use ledger device for signing transactions")
	flag.StringVar(&mnemonic, "mnemonic", "", "Mnemonic to use for signing transactions")
	flag.StringVar(&hdPath, "hd-path", "m/44'/60'/0'/0/0", "Hierarchical deterministic derivation path for mnemonic or ledger")
	flag.Parse()

	log.Default().SetFlags(0)

	n, ok := networks[networkFlag]
	if !ok {
		log.Fatalf("Unknown network: %s", networkFlag)
	}

	if l2RpcFlag != "" || portalAddress != "" || l2OOAddress != "" {
		if l2RpcFlag == "" {
			log.Fatalf("Missing --l2-rpc flag")
		}
		if portalAddress == "" {
			log.Fatalf("Missing --portal-address flag")
		}
		if l2OOAddress == "" {
			log.Fatalf("Missing --l2oo-address flag")
		}
		n = network{
			l2RPC:         l2RpcFlag,
			portalAddress: portalAddress,
			l2OOAddress:   l2OOAddress,
		}
	}

	if rpcFlag == "" {
		log.Fatalf("Missing --rpc flag")
	}

	if withdrawalFlag == "" {
		log.Fatalf("Missing --withdrawal flag")
	}
	withdrawal := common.HexToHash(withdrawalFlag)

	options := 0
	if privateKey != "" {
		options++
	}
	if ledger {
		options++
	}
	if mnemonic != "" {
		options++
	}
	if options != 1 {
		log.Fatalf("One (and only one) of --private-key, --ledger, --mnemonic must be set")
	}

	s, err := signer.CreateSigner(privateKey, mnemonic, hdPath)
	if err != nil {
		log.Fatalf("Error creating signer: %v", err)
	}

	ctx := context.Background()

	l1Client, err := ethclient.DialContext(ctx, rpcFlag)
	if err != nil {
		log.Fatalf("Error dialing L1 client: %v", err)
	}

	l1ChainID, err := l1Client.ChainID(ctx)
	if err != nil {
		log.Fatalf("Error querying chain ID: %v", err)
	}

	l1Nonce, err := l1Client.PendingNonceAt(ctx, s.Address())
	if err != nil {
		log.Fatalf("Error querying nonce: %v", err)
	}

	l1opts := &bind.TransactOpts{
		From:    s.Address(),
		Signer:  s.SignerFn(l1ChainID),
		Context: ctx,
		Nonce:   big.NewInt(int64(l1Nonce) - 1), // subtract 1 because we add 1 each time newl1opts is called
	}
	newl1opts := func() *bind.TransactOpts {
		l1opts.Nonce = big.NewInt(0).Add(l1opts.Nonce, big.NewInt(1))
		return l1opts
	}

	l2Client, err := rpc.DialContext(ctx, n.l2RPC)
	if err != nil {
		log.Fatalf("Error dialing L2 client: %v", err)
	}

	portal, err := bindings.NewOptimismPortal(common.HexToAddress(n.portalAddress), l1Client)
	if err != nil {
		log.Fatalf("Error binding OptimismPortal contract: %v", err)
	}

	l2oo, err := bindings.NewL2OutputOracle(common.HexToAddress(n.l2OOAddress), l1Client)
	if err != nil {
		log.Fatalf("Error binding L2OutputOracle contract: %v", err)
	}

	finalizationPeriod, err := l2oo.FINALIZATIONPERIODSECONDS(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("Error querying withdrawal finalization period: %v", err)
	}

	submissionInterval, err := l2oo.SUBMISSIONINTERVAL(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("Error querying output proposal submission interval: %v", err)
	}

	l2OutputBlock, err := l2oo.LatestBlockNumber(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("Error querying latest proposed block: %v", err)
	}

	l2WithdrawalBlock, err := withdraw.TxBlock(ctx, l2Client, withdrawal)
	if err != nil {
		log.Fatalf("Error querying withdrawal tx block: %v", err)
	}

	if l2OutputBlock.Uint64() < l2WithdrawalBlock.Uint64() {
		log.Fatalf("The latest L2 output is %d and is not past L2 block %d that includes the withdrawal, no withdrawal can be proved yet.\nPlease wait for the next proposal submission to %s, which happens every %v.",
			l2OutputBlock.Uint64(), l2WithdrawalBlock.Uint64(), n.l2OOAddress, time.Duration(submissionInterval.Int64())*time.Second)
	}

	proof, err := withdraw.ProvenWithdrawal(ctx, l2Client, portal, withdrawal)
	if err != nil {
		log.Fatalf("Error querying withdrawal proof: %v", err)
	}

	if proof.Timestamp.Uint64() == 0 {
		err = withdraw.ProveWithdrawal(ctx, l1Client, l2Client, l2oo, portal, withdrawal, newl1opts())
		if err != nil {
			log.Fatalf("Error proving withdrawal: %v", err)
		}
		fmt.Printf("The withdrawal can be completed after the finalization period, in approximately %v\n", time.Duration(finalizationPeriod.Int64())*time.Second)
		return
	}

	err = withdraw.CompleteWithdrawal(ctx, l1Client, l2Client, l2oo, portal, withdrawal, finalizationPeriod, newl1opts())
	if err != nil {
		log.Fatalf("Error completing withdrawal: %v", err)
	}
}
