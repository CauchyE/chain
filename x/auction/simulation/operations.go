package simulation

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"time"

	appparams "github.com/UnUniFi/chain/app/params"
	"github.com/UnUniFi/chain/x/auction/keeper"
	"github.com/UnUniFi/chain/x/auction/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/simapp/helpers"
	simappparams "github.com/cosmos/cosmos-sdk/simapp/params"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	"github.com/cosmos/cosmos-sdk/x/simulation"
)

var (
	errorNotEnoughCoins  = errors.New("account doesn't have enough coins")
	errorCantReceiveBids = errors.New("auction can't receive bids (lot = 0 in reverse auction)")
)

// Simulation operation weights constants
const (
	OpWeightMsgPlaceBid = "op_weight_msg_place_bid"
)

// WeightedOperations returns all the operations from the module with their respective weights
func WeightedOperations(
	appParams simtypes.AppParams, simState module.SimulationState, ak authkeeper.AccountKeeper, k keeper.Keeper, ack types.AccountKeeper,
	bk bankkeeper.Keeper,
) simulation.WeightedOperations {
	var weightMsgPlaceBid int
	appParams.GetOrGenerate(simState.Cdc, OpWeightMsgPlaceBid, &weightMsgPlaceBid, nil,
		func(_ *rand.Rand) {
			weightMsgPlaceBid = appparams.DefaultWeightMsgPlaceBid
		},
	)

	return simulation.WeightedOperations{
		simulation.NewWeightedOperation(
			weightMsgPlaceBid,
			SimulateMsgPlaceBid(ak, bk, k),
		),
	}
}

func onceInit(ctx sdk.Context, bk bankkeeper.Keeper, bidderAddr sdk.AccAddress) {
	// todo get upper
	// simState.Accounts[0].Address
	iniCoins := []sdk.Coin{
		sdk.NewInt64Coin("usdx", 100),
		sdk.NewInt64Coin("uguu", 1000000000000),
		sdk.NewInt64Coin("debt", 100),
	}
	// maybe change bank
	bk.MintCoins(ctx, types.ModuleName, sdk.Coins(iniCoins))
	bk.SendCoinsFromModuleToAccount(ctx, types.ModuleName, bidderAddr, sdk.NewCoins(sdk.NewInt64Coin("usdx", 100)))
}

// SimulateMsgPlaceBid returns a function that runs a random state change on the module keeper.
// There's two error paths
// - return a OpMessage, but nil error - this will log a message but keep running the simulation
// - return an error - this will stop the simulation
func SimulateMsgPlaceBid(ak authkeeper.AccountKeeper, bk bankkeeper.Keeper, keeper keeper.Keeper) simtypes.Operation {
	return func(
		r *rand.Rand, app *baseapp.BaseApp, ctx sdk.Context, accs []simtypes.Account, chainID string,
	) (simtypes.OperationMsg, []simtypes.FutureOperation, error) {
		// get open auctions
		openAuctions := types.Auctions{}
		keeper.IterateAuctions(ctx, func(a types.Auction) bool {
			openAuctions = append(openAuctions, a)
			return false
		})

		// shuffle auctions slice so that bids are evenly distributed across auctions
		r.Shuffle(len(openAuctions), func(i, j int) {
			openAuctions[i], openAuctions[j] = openAuctions[j], openAuctions[i]
		})

		// search through auctions and accounts to find a pair where a bid can be placed (ie account has enough coins to place bid on auction)
		blockTime := ctx.BlockHeader().Time
		params := keeper.GetParams(ctx)
		bidder, openAuction, found := findValidAccountAuctionPair(accs, openAuctions, func(acc simtypes.Account, auc types.Auction) bool {
			account := ak.GetAccount(ctx, acc.Address)
			accuontAmount := bk.SpendableCoins(ctx, account.GetAddress())
			_, err := generateBidAmount(r, params, auc, accuontAmount, blockTime)
			if err == errorNotEnoughCoins || err == errorCantReceiveBids {
				return false // keep searching
			} else if err != nil {
				panic(err) // raise errors
			}
			return true // found valid pair
		})
		if !found {
			return simtypes.NewOperationMsgBasic(types.ModuleName, "no-operation (no valid auction and bidder)", "", false, nil), nil, nil
		}

		bidderAcc := ak.GetAccount(ctx, bidder.Address)
		if bidderAcc == nil {
			return simtypes.NoOpMsg(types.ModuleName, types.TypeMsgPlaceBid, "bidderAcc is nil"), nil, fmt.Errorf("couldn't find account %s", bidder.Address)
		}

		// pick a bid amount for the chosen auction and bidder

		bidderAmount := bk.SpendableCoins(ctx, bidderAcc.GetAddress())
		amount, err := generateBidAmount(r, params, openAuction, bidderAmount, blockTime)
		if err != nil { // shouldn't happen given the checks above
			return simtypes.NoOpMsg(types.ModuleName, types.TypeMsgPlaceBid, err.Error()), nil, err
		}

		// create and deliver a tx
		msg := types.NewMsgPlaceBid(openAuction.GetID(), bidder.Address, amount)

		txGen := simappparams.MakeTestEncodingConfig().TxConfig
		tx, err := helpers.GenTx(
			txGen,
			[]sdk.Msg{&msg},
			sdk.NewCoins(), // TODO pick a random amount fees
			helpers.DefaultGenTxGas,
			chainID,
			[]uint64{bidderAcc.GetAccountNumber()},
			[]uint64{bidderAcc.GetSequence()},
			bidder.PrivKey,
		)
		if err != nil {
			return simtypes.NoOpMsg(types.ModuleName, types.TypeMsgPlaceBid, err.Error()), nil, err
		}

		_, _, err = app.Deliver(txGen.TxEncoder(), tx)
		if err != nil {
			// to aid debugging, add the stack trace to the comment field of the returned opMsg
			return simtypes.NewOperationMsg(&msg, false, fmt.Sprintf("%+v", err), nil), nil, err
		}
		return simtypes.NewOperationMsg(&msg, true, "", nil), nil, nil
	}
}

func generateBidAmount(
	r *rand.Rand, params types.Params, auc types.Auction,
	bidderBalance sdk.Coins, blockTime time.Time) (sdk.Coin, error) {
	// bidderBalance
	//  := bidder.SpendableCoins(blockTime) // return coins

	switch a := auc.(type) {

	case *types.DebtAuction:
		// Check bidder has enough (stable coin) to pay in
		if bidderBalance.AmountOf(a.Bid.Denom).LT(a.Bid.Amount) { // stable coin
			return sdk.Coin{}, errorNotEnoughCoins
		}
		// Check auction can still receive new bids
		if a.Lot.Amount.Equal(sdk.ZeroInt()) {
			return sdk.Coin{}, errorCantReceiveBids
		}
		// Generate a new lot amount (gov coin)
		maxNewLotAmt := a.Lot.Amount.Sub( // new lot must be some % less than old lot, and at least 1 smaller to avoid replacing an old bid at no cost
			sdk.MaxInt(
				sdk.NewInt(1),
				sdk.NewDecFromInt(a.Lot.Amount).Mul(params.IncrementDebt).RoundInt(),
			),
		)
		amt, err := RandIntInclusive(r, sdk.ZeroInt(), maxNewLotAmt) // maxNewLotAmt shouldn't be < 0 given the check above
		if err != nil {
			panic(err)
		}
		return sdk.NewCoin(a.Lot.Denom, amt), nil // gov coin

	case *types.SurplusAuction:
		// Check the bidder has enough (gov coin) to pay in
		minNewBidAmt := a.Bid.Amount.Add( // new bids must be some % greater than old bid, and at least 1 larger to avoid replacing an old bid at no cost
			sdk.MaxInt(
				sdk.NewInt(1),
				sdk.NewDecFromInt(a.Bid.Amount).Mul(params.IncrementSurplus).RoundInt(),
			),
		)
		if bidderBalance.AmountOf(a.Bid.Denom).LT(minNewBidAmt) { // gov coin
			return sdk.Coin{}, errorNotEnoughCoins
		}
		// Generate a new bid amount (gov coin)
		amt, err := RandIntInclusive(r, minNewBidAmt, bidderBalance.AmountOf(a.Bid.Denom))
		if err != nil {
			panic(err)
		}
		return sdk.NewCoin(a.Bid.Denom, amt), nil // gov coin

	case *types.CollateralAuction:
		// Check the bidder has enough (stable coin) to pay in
		minNewBidAmt := a.Bid.Amount.Add( // new bids must be some % greater than old bid, and at least 1 larger to avoid replacing an old bid at no cost
			sdk.MaxInt(
				sdk.NewInt(1),
				sdk.NewDecFromInt(a.Bid.Amount).Mul(params.IncrementCollateral).RoundInt(),
			),
		)
		minNewBidAmt = sdk.MinInt(minNewBidAmt, a.MaxBid.Amount) // allow new bids to hit MaxBid even though it may be less than the increment %
		if bidderBalance.AmountOf(a.Bid.Denom).LT(minNewBidAmt) {
			return sdk.Coin{}, errorNotEnoughCoins
		}
		// Check auction can still receive new bids
		if a.IsReversePhase() && a.Lot.Amount.Equal(sdk.ZeroInt()) {
			return sdk.Coin{}, errorCantReceiveBids
		}
		// Generate a new bid amount (collateral coin in reverse phase)
		if a.IsReversePhase() {
			maxNewLotAmt := a.Lot.Amount.Sub( // new lot must be some % less than old lot, and at least 1 smaller to avoid replacing an old bid at no cost
				sdk.MaxInt(
					sdk.NewInt(1),
					sdk.NewDecFromInt(a.Lot.Amount).Mul(params.IncrementCollateral).RoundInt(),
				),
			)
			amt, err := RandIntInclusive(r, sdk.ZeroInt(), maxNewLotAmt) // maxNewLotAmt shouldn't be < 0 given the check above
			if err != nil {
				panic(err)
			}
			return sdk.NewCoin(a.Lot.Denom, amt), nil // collateral coin

			// Generate a new bid amount (stable coin in forward phase)
		} else {
			amt, err := RandIntInclusive(r, minNewBidAmt, sdk.MinInt(bidderBalance.AmountOf(a.Bid.Denom), a.MaxBid.Amount))
			if err != nil {
				panic(err)
			}
			// when the bidder has enough coins, pick the MaxBid amount more frequently to increase chance auctions phase get into reverse phase
			if r.Intn(2) == 0 && bidderBalance.AmountOf(a.Bid.Denom).GTE(a.MaxBid.Amount) { // 50%
				amt = a.MaxBid.Amount
			}
			return sdk.NewCoin(a.Bid.Denom, amt), nil // stable coin
		}

	default:
		return sdk.Coin{}, fmt.Errorf("unknown auction type")
	}
}

// findValidAccountAuctionPair finds an auction and account for which the callback func returns true
func findValidAccountAuctionPair(accounts []simtypes.Account, auctions types.Auctions, cb func(simtypes.Account, types.Auction) bool) (simtypes.Account, types.Auction, bool) {
	for _, auc := range auctions {
		for _, acc := range accounts {
			if isValid := cb(acc, auc); isValid {
				return acc, auc, true
			}
		}
	}
	return simtypes.Account{}, nil, false
}

// RandIntInclusive randomly generates an sdk.Int in the range [inclusiveMin, inclusiveMax]. It works for negative and positive integers.
func RandIntInclusive(r *rand.Rand, inclusiveMin, inclusiveMax sdk.Int) (sdk.Int, error) {
	if inclusiveMin.GT(inclusiveMax) {
		return sdk.Int{}, fmt.Errorf("min larger than max")
	}
	return RandInt(r, inclusiveMin, inclusiveMax.Add(sdk.OneInt()))
}

// RandInt randomly generates an sdk.Int in the range [inclusiveMin, exclusiveMax). It works for negative and positive integers.
func RandInt(r *rand.Rand, inclusiveMin, exclusiveMax sdk.Int) (sdk.Int, error) {
	// validate input
	if inclusiveMin.GTE(exclusiveMax) {
		return sdk.Int{}, fmt.Errorf("min larger or equal to max")
	}
	// shift the range to start at 0
	shiftedRange := exclusiveMax.Sub(inclusiveMin) // should always be positive given the check above
	// randomly pick from the shifted range
	shiftedRandInt := sdk.NewIntFromBigInt(new(big.Int).Rand(r, shiftedRange.BigInt()))
	// shift back to the original range
	return shiftedRandInt.Add(inclusiveMin), nil
}
