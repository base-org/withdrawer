package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-bindings/bindings"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/base-org/withdrawer/signer"
	"github.com/base-org/withdrawer/withdraw"
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
	"base-sepolia": {
		l2RPC:         "https://sepolia.base.org",
		portalAddress: "0x49f53e41452C74589E85cA1677426Ba426459e85",
		l2OOAddress:   "0x84457ca9D0163FbC4bbfe4Dfbb20ba46e48DF254",
	},
	"op-mainnet": {
		l2RPC:         "https://mainnet.optimism.io",
		portalAddress: "0xbEb5Fc579115071764c7423A4f12eDde41f106Ed",
		l2OOAddress:   "0xdfe97868233d1aa22e815a266982f2cf17685a27",
	},
	"op-sepolia": {
		l2RPC:         "https://sepolia.optimism.io",
		portalAddress: "0x16Fc5058F25648194471939df75CF27A2fdC48BC",
		l2OOAddress:   "0x90E9c4f8a994a250F6aEfd61CAFb4F2e895D458F",
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

	log.SetDefault(oplog.NewLogger(os.Stderr, oplog.DefaultCLIConfig()))

	n, ok := networks[networkFlag]
	if !ok {
		log.Crit("Unknown network", "network", networkFlag)
	}

	if l2RpcFlag != "" || portalAddress != "" || l2OOAddress != "" {
		if l2RpcFlag == "" {
			log.Crit("Missing --l2-rpc flag")
		}
		if portalAddress == "" {
			log.Crit("Missing --portal-address flag")
		}
		if l2OOAddress == "" {
			log.Crit("Missing --l2oo-address flag")
		}
		n = network{
			l2RPC:         l2RpcFlag,
			portalAddress: portalAddress,
			l2OOAddress:   l2OOAddress,
		}
	}

	if rpcFlag == "" {
		log.Crit("Missing --rpc flag")
	}

	if withdrawalFlag == "" {
		log.Crit("Missing --withdrawal flag")
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
		log.Crit("One (and only one) of --private-key, --ledger, --mnemonic must be set")
	}

	s, err := signer.CreateSigner(privateKey, mnemonic, hdPath)
	if err != nil {
		log.Crit("Error creating signer", "error", err)
	}

	ctx := context.Background()

	l1Client, err := ethclient.DialContext(ctx, rpcFlag)
	if err != nil {
		log.Crit("Error dialing L1 client", "error", err)
	}

	l1ChainID, err := l1Client.ChainID(ctx)
	if err != nil {
		log.Crit("Error querying chain ID", "error", err)
	}

	l1Nonce, err := l1Client.PendingNonceAt(ctx, s.Address())
	if err != nil {
		log.Crit("Error querying nonce", "error", err)
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
		log.Crit("Error dialing L2 client", "error", err)
	}

	portal, err := bindings.NewOptimismPortal(common.HexToAddress(n.portalAddress), l1Client)
	if err != nil {
		log.Crit("Error binding OptimismPortal contract", "error", err)
	}

	l2oo, err := bindings.NewL2OutputOracle(common.HexToAddress(n.l2OOAddress), l1Client)
	if err != nil {
		log.Crit("Error binding L2OutputOracle contract", "error", err)
	}

	isFinalized, err := withdraw.ProofFinalized(ctx, portal, withdrawal)
	if err != nil {
		log.Crit("Error querying withdrawal finalization status", "error", err)
	}
	if isFinalized {
		fmt.Println("Withdrawal already finalized")
		return
	}

	finalizationPeriod, err := l2oo.FINALIZATIONPERIODSECONDS(&bind.CallOpts{})
	if err != nil {
		log.Crit("Error querying withdrawal finalization period", "error", err)
	}

	submissionInterval, err := l2oo.SUBMISSIONINTERVAL(&bind.CallOpts{})
	if err != nil {
		log.Crit("Error querying output proposal submission interval", "error", err)
	}

	l2BlockTime, err := l2oo.L2BLOCKTIME(&bind.CallOpts{})
	if err != nil {
		log.Crit("Error querying output proposal L2 block time", "error", err)
	}

	l2OutputBlock, err := l2oo.LatestBlockNumber(&bind.CallOpts{})
	if err != nil {
		log.Crit("Error querying latest proposed block", "error", err)
	}

	l2WithdrawalBlock, err := withdraw.TxBlock(ctx, l2Client, withdrawal)
	if err != nil {
		log.Crit("Error querying withdrawal tx block", "error", err)
	}

	if l2OutputBlock.Uint64() < l2WithdrawalBlock.Uint64() {
		log.Crit(fmt.Sprintf("The latest L2 output is %d and is not past L2 block %d that includes the withdrawal, no withdrawal can be proved yet.\nPlease wait for the next proposal submission to %s, which happens every %v.",
			l2OutputBlock.Uint64(), l2WithdrawalBlock.Uint64(), n.l2OOAddress, time.Duration(submissionInterval.Int64()*l2BlockTime.Int64())*time.Second))
	}

	proof, err := withdraw.ProvenWithdrawal(ctx, l2Client, portal, withdrawal)
	if err != nil {
		log.Crit("Error querying withdrawal proof", "error", err)
	}

	if proof.Timestamp.Uint64() == 0 {
		err = withdraw.ProveWithdrawal(ctx, l1Client, l2Client, l2oo, portal, withdrawal, newl1opts())
		if err != nil {
			log.Crit("Error proving withdrawal", "error", err)
		}
		fmt.Printf("The withdrawal can be completed after the finalization period, in approximately %v\n", time.Duration(finalizationPeriod.Int64())*time.Second)
		return
	}

	err = withdraw.CompleteWithdrawal(ctx, l1Client, l2Client, l2oo, portal, withdrawal, finalizationPeriod, newl1opts())
	if err != nil {
		log.Crit("Error completing withdrawal", "error", err)
	}
}
