package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-bitfield"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/lotus/api/v0api"
	"github.com/filecoin-project/lotus/blockstore"
	"github.com/filecoin-project/lotus/chain/actors/adt"
	"github.com/filecoin-project/lotus/chain/actors/builtin/miner"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/mitchellh/go-homedir"
	"github.com/urfave/cli/v2"
	"golang.org/x/xerrors"

	miner7 "github.com/filecoin-project/specs-actors/v7/actors/builtin/miner"
	smoothing7 "github.com/filecoin-project/specs-actors/v7/actors/util/smoothing"
)

func readline(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	rd := bufio.NewReader(f)

	var ret = []string{}
	for {
		line, err := rd.ReadString('\n') //以'\n'为结束符读入一行

		if err != nil || io.EOF == err {
			break
		}

		line = strings.Replace(line, "\n", "", -1)

		ret = append(ret, line)
	}

	return ret
}

var terminateAllCmd = &cli.Command{
	Name:  "terminateAll",
	Usage: "terminate all active sectors",
	Subcommands: []*cli.Command{
		terminateBalanceCmd,
		// terminateProfitCmd,
	},
}

var terminateBalanceCmd = &cli.Command{
	Name:  "balance",
	Usage: "compute miner balance after terminate all It's sectors",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "actor",
			Usage: "specify the address of miner actor",
		},
		&cli.StringFlag{
			Name:  "miners-path",
			Usage: "specify miners path to read",
		},
		&cli.StringFlag{
			Name:  "sectors",
			Usage: "specify sector to teminate",
		},
		&cli.StringFlag{
			Name:  "sectors-path",
			Usage: "specify sectors path to read",
		},
	},
	Action: func(cctx *cli.Context) error {
		var actors []address.Address
		var sectors []abi.SectorNumber

		if cctx.String("miners-path") == "" && cctx.String("actor") == "" {
			return xerrors.New("miner-path and actor must not emoty at the same time")
		}

		if cctx.String("miners-path") != "" {
			path, err := homedir.Expand(cctx.String("miners-path"))
			if err != nil {
				return err
			}
			acts := readline(path)

			for _, ac := range acts {
				ma, err := address.NewFromString(ac)
				if err != nil {
					return fmt.Errorf("parsing address %s: %w", ac, err)
				}
				actors = append(actors, ma)
			}
		} else if cctx.String("actor") != "" {
			var maddr address.Address
			if act := cctx.String("actor"); act != "" {
				var err error
				maddr, err = address.NewFromString(act)
				if err != nil {
					return fmt.Errorf("parsing address %s: %w", act, err)
				}
			} else {
				return fmt.Errorf("actor address must provide")
			}
			actors = append(actors, maddr)
		}

		if len(actors) == 0 {
			return xerrors.New("actors must provide")
		}

		if cctx.String("sectors-path") != "" {
			path, err := homedir.Expand(cctx.String("miners-path"))
			if err != nil {
				return err
			}
			sectorss := readline(path)

			for _, ss := range sectorss {
				ssi, err := strconv.ParseUint(ss, 10, 64)
				if err != nil {
					return err
				}
				sectors = append(sectors, abi.SectorNumber(ssi))
			}
		} else if cctx.String("sectors") != "" {
			var sectorStrs []string
			if strings.Contains(cctx.String("sectors"), ",") {
				sectorStrs = strings.Split(cctx.String("sectors"), ",")
			} else {
				sectorStrs = append(sectorStrs, cctx.String("sectors"))
			}

			if len(sectorStrs) == 0 {
				return xerrors.Errorf("sectors length is empty")
			}

			for _, str := range sectorStrs {
				ssi, err := strconv.ParseUint(str, 10, 64)
				if err != nil {
					return err
				}
				sectors = append(sectors, abi.SectorNumber(ssi))
			}
		}

		tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "epoch\tminer\t\tssize\t\tsectornum\t\tpledge\t\tdeposit\t\tvesting\t\tavailable\t\tpenlty\t\tpledgeelta")

		nodeAPI, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			return err
		}
		defer acloser()

		ctx := lcli.ReqContext(cctx)

		for _, actor := range actors {
			err := calculateBalance(ctx, nodeAPI, actor, sectors, tw)
			if err != nil {
				return err
			}
		}
		return nil
	},
}

func calculateBalance(ctx context.Context, nodeAPI v0api.FullNode, maddr address.Address, sectors []abi.SectorNumber, tw *tabwriter.Writer) error {
	mi, err := nodeAPI.StateMinerInfo(ctx, maddr, types.EmptyTSK)
	if err != nil {
		return err
	}

	var sinfos = []*miner.SectorOnChainInfo{}
	if len(sectors) > 0 {
		allsectors := bitfield.New()
		for _, ss := range sectors {
			allsectors.Set(uint64(ss))
		}

		sins, err := nodeAPI.StateMinerSectors(ctx, maddr, &allsectors, types.EmptyTSK)
		if err != nil {
			return err
		}
		sinfos = append(sinfos, sins...)
	} else {
		sins, err := nodeAPI.StateMinerActiveSectors(ctx, maddr, types.EmptyTSK)
		if err != nil {
			return err
		}
		sinfos = append(sinfos, sins...)
	}

	if len(sinfos) == 0 {
		return xerrors.Errorf("miner no sectors to lookup")
	}

	head, err := nodeAPI.ChainHead(ctx)
	if err != nil {
		return err
	}

	////////////////////////////////

	rewardAddr, err := address.NewFromString("f02")
	if err != nil {
		return err
	}
	rSt, err := nodeAPI.StateReadState(ctx, rewardAddr, head.Key())
	if err != nil {
		return err
	}
	rewardState, ok := rSt.State.(map[string]interface{})
	if !ok {
		return xerrors.Errorf("internal error: failed to cast reward state to expected map type")
	}
	epochRewardIface, ok := rewardState["ThisEpochRewardSmoothed"]
	if !ok {
		return xerrors.Errorf("reward %s had no ThisEpochRewardSmoothed state, is this a v7 state root?", rewardAddr)
	}
	// smoothing.FilterEstimate
	rewardStats := epochRewardIface.(map[string]interface{})

	p, ok := rewardStats["PositionEstimate"]
	if !ok {
		return xerrors.Errorf("internal error: failed to cast reward state to expected PositionEstimate type")
	}

	v, ok := rewardStats["VelocityEstimate"]
	if !ok {
		return xerrors.Errorf("internal error: failed to cast reward state to expected VelocityEstimate type")
	}

	fe := smoothing7.FilterEstimate{
		PositionEstimate: big.MustFromString(p.(string)),
		VelocityEstimate: big.MustFromString(v.(string)),
	}

	//////////////////////////////

	powerAddr, err := address.NewFromString("f04")
	if err != nil {
		return err
	}
	pSt, err := nodeAPI.StateReadState(ctx, powerAddr, head.Key())
	if err != nil {
		return err
	}
	powerState, ok := pSt.State.(map[string]interface{})
	if !ok {
		return xerrors.Errorf("internal error: failed to cast power state to expected map type")
	}

	epochPowerIface, ok := powerState["ThisEpochQAPowerSmoothed"]
	if !ok {
		return xerrors.Errorf("reward %s had no ThisEpochRewardSmoothed state, is this a v5 state root?", rewardAddr)
	}

	epochPowerMap, ok := epochPowerIface.(map[string]interface{})
	if !ok {
		return xerrors.Errorf("internal error: failed to cast power state to expected map type")
	}

	p1, ok := epochPowerMap["PositionEstimate"]
	if !ok {
		return xerrors.Errorf("internal error: failed to cast reward state to expected PositionEstimate type")
	}

	v1, ok := epochPowerMap["VelocityEstimate"]
	if !ok {
		return xerrors.Errorf("internal error: failed to cast reward state to expected VelocityEstimate type")
	}

	pwrTotal := smoothing7.FilterEstimate{
		PositionEstimate: big.MustFromString(p1.(string)),
		VelocityEstimate: big.MustFromString(v1.(string)),
	}

	////////////////////////////

	penalty := big.Zero()
	ipDelta := big.Zero()

	for _, info := range sinfos {
		miner_info := &miner7.SectorOnChainInfo{
			SectorNumber:          info.SectorNumber,
			SealProof:             info.SealProof,
			SealedCID:             info.SealedCID,
			DealIDs:               info.DealIDs,
			Activation:            info.Activation,
			Expiration:            info.Expiration,
			DealWeight:            info.DealWeight,
			VerifiedDealWeight:    info.VerifiedDealWeight,
			InitialPledge:         info.InitialPledge,
			ExpectedDayReward:     info.ExpectedDayReward,
			ExpectedStoragePledge: info.ExpectedStoragePledge,

			ReplacedSectorAge: abi.ChainEpoch(0),
			ReplacedDayReward: big.Zero(),
		}

		ipDelta = big.Add(ipDelta, miner_info.InitialPledge)

		sectorPower := miner7.QAPowerForSector(mi.SectorSize, miner_info)
		fee := miner7.PledgePenaltyForTermination(miner_info.ExpectedDayReward, head.Height()-miner_info.Activation, miner_info.ExpectedStoragePledge,
			pwrTotal, sectorPower, fe, miner_info.ReplacedDayReward, miner_info.ReplacedSectorAge)

		fmt.Printf("sector: %d ExpDayReward: %s cur: %s act: %s sPower:%s fee: %s\n",
			miner_info.SectorNumber, types.FIL(miner_info.ExpectedDayReward).Short(), head.Height().String(), miner_info.Activation.String(), sectorPower.String(), fee)

		penalty = big.Add(penalty, fee)
	}

	mact, err := nodeAPI.StateGetActor(ctx, maddr, head.Key())
	if err != nil {
		return err
	}

	tbs := blockstore.NewTieredBstore(blockstore.NewAPIBlockstore(nodeAPI), blockstore.NewMemory())
	mas, err := miner.Load(adt.WrapStore(ctx, cbor.NewCborStore(tbs)), mact)
	if err != nil {
		return err
	}

	// right now

	lockedFunds, err := mas.LockedFunds()
	if err != nil {
		return xerrors.Errorf("getting locked funds: %w", err)
	}
	availBalance, err := mas.AvailableBalance(mact.Balance)
	if err != nil {
		return xerrors.Errorf("getting available balance: %w", err)
	}

	// after action

	realpenalty := penalty

	ipAfter := big.Sub(lockedFunds.InitialPledgeRequirement, ipDelta)
	depositAfter := lockedFunds.PreCommitDeposits

	vestingAfter := big.Zero()
	avaAfter := big.Zero()
	if lockedFunds.VestingFunds.GreaterThanEqual(penalty) {
		vestingAfter = big.Sub(lockedFunds.VestingFunds, penalty)
		avaAfter = big.Add(availBalance, ipDelta)
	} else {
		delta := big.Sub(penalty, lockedFunds.VestingFunds)
		avaAfter = big.Add(availBalance, ipDelta)
		avaAfter = big.Sub(avaAfter, delta)
	}

	_, _ = fmt.Fprintf(tw, "%s\t\t%s\t\t%d\t\t%d\t\t%s\t\t%s\t\t%s\t\t%s\t\t%s\t\t%s\n",
		head.Height().String(),
		maddr,
		mi.SectorSize,
		len(sinfos),

		types.FIL(ipAfter).Short(),
		types.FIL(depositAfter).Short(),

		types.FIL(vestingAfter).Short(),
		types.FIL(avaAfter).Short(),

		types.FIL(realpenalty).Short(),
		types.FIL(ipDelta).Short())

	return tw.Flush()
}
