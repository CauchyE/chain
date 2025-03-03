package types

import (
	"testing"
	"time"

	"github.com/UnUniFi/chain/types"
	"github.com/stretchr/testify/require"

	tmtypes "github.com/tendermint/tendermint/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func TestGenesisStateValidate(t *testing.T) {
	now := time.Now()
	mockPrivKey := tmtypes.NewMockPV()
	pubkey, err := mockPrivKey.GetPubKey()
	require.NoError(t, err)
	addr := sdk.AccAddress(pubkey.Address())

	testCases := []struct {
		msg          string
		genesisState GenesisState
		expPass      bool
	}{
		{
			msg:          "default",
			genesisState: *DefaultGenesis(),
			expPass:      true,
		},
		{
			msg: "valid genesis",
			genesisState: NewGenesisState(
				NewParams(Markets{
					{"market", "xrp", "bnb", types.StringAccAddresses([]sdk.AccAddress{addr}), true},
				}),
				[]PostedPrice{NewPostedPrice("xrp", addr, sdk.OneDec(), now)},
			),
			expPass: true,
		},
		{
			msg: "invalid param",
			genesisState: NewGenesisState(
				NewParams(Markets{
					{"", "xrp", "bnb", types.StringAccAddresses([]sdk.AccAddress{addr}), true},
				}),
				[]PostedPrice{NewPostedPrice("xrp", addr, sdk.OneDec(), now)},
			),
			expPass: false,
		},
		{
			msg: "dup market param",
			genesisState: NewGenesisState(
				NewParams(Markets{
					{"market", "xrp", "bnb", types.StringAccAddresses([]sdk.AccAddress{addr}), true},
					{"market", "xrp", "bnb", types.StringAccAddresses([]sdk.AccAddress{addr}), true},
				}),
				[]PostedPrice{NewPostedPrice("xrp", addr, sdk.OneDec(), now)},
			),
			expPass: false,
		},
		{
			msg: "invalid posted price",
			genesisState: NewGenesisState(
				NewParams(Markets{}),
				[]PostedPrice{NewPostedPrice("xrp", nil, sdk.OneDec(), now)},
			),
			expPass: false,
		},
		{
			msg: "duplicated posted price",
			genesisState: NewGenesisState(
				NewParams(Markets{}),
				[]PostedPrice{
					NewPostedPrice("xrp", addr, sdk.OneDec(), now),
					NewPostedPrice("xrp", addr, sdk.OneDec(), now),
				},
			),
			expPass: false,
		},
	}

	for _, tc := range testCases {
		err := tc.genesisState.Validate()
		if tc.expPass {
			require.NoError(t, err, tc.msg)
		} else {
			require.Error(t, err, tc.msg)
		}
	}
}
