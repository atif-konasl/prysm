package beacon

import (
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"testing"

	"github.com/gogo/protobuf/proto"
	types "github.com/prysmaticlabs/eth2-types"
	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	mock "github.com/prysmaticlabs/prysm/beacon-chain/blockchain/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	dbTest "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/state/stategen"
	"github.com/prysmaticlabs/prysm/shared/cmd"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	"github.com/prysmaticlabs/prysm/shared/testutil/assert"
	"github.com/prysmaticlabs/prysm/shared/testutil/require"
)

func TestServer_ListAssignments_CannotRequestFutureEpoch(t *testing.T) {

	db := dbTest.SetupDB(t)
	ctx := context.Background()
	bs := &Server{
		BeaconDB:           db,
		GenesisTimeFetcher: &mock.ChainService{},
	}

	wanted := errNoEpochInfoError
	_, err := bs.ListValidatorAssignments(
		ctx,
		&ethpb.ListValidatorAssignmentsRequest{
			QueryFilter: &ethpb.ListValidatorAssignmentsRequest_Epoch{
				Epoch: helpers.SlotToEpoch(bs.GenesisTimeFetcher.CurrentSlot()) + 1,
			},
		},
	)
	assert.ErrorContains(t, wanted, err)
}

func TestServer_ListAssignments_NoResults(t *testing.T) {

	db := dbTest.SetupDB(t)
	ctx := context.Background()
	st, err := testutil.NewBeaconState()
	require.NoError(t, err)

	b := testutil.NewBeaconBlock()
	require.NoError(t, db.SaveBlock(ctx, b))
	gRoot, err := b.Block.HashTreeRoot()
	require.NoError(t, err)
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, gRoot))
	require.NoError(t, db.SaveState(ctx, st, gRoot))

	bs := &Server{
		BeaconDB:           db,
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}
	wanted := &ethpb.ValidatorAssignments{
		Assignments:   make([]*ethpb.ValidatorAssignments_CommitteeAssignment, 0),
		TotalSize:     int32(0),
		NextPageToken: strconv.Itoa(0),
	}
	res, err := bs.ListValidatorAssignments(
		ctx,
		&ethpb.ListValidatorAssignmentsRequest{
			QueryFilter: &ethpb.ListValidatorAssignmentsRequest_Genesis{
				Genesis: true,
			},
		},
	)
	require.NoError(t, err)
	if !proto.Equal(wanted, res) {
		t.Errorf("Wanted %v, received %v", wanted, res)
	}
}

func TestServer_ListAssignments_Pagination_InputOutOfRange(t *testing.T) {
	helpers.ClearCache()
	db := dbTest.SetupDB(t)
	ctx := context.Background()
	count := 100
	validators := make([]*ethpb.Validator, 0, count)
	for i := 0; i < count; i++ {
		pubKey := make([]byte, params.BeaconConfig().BLSPubkeyLength)
		withdrawalCred := make([]byte, 32)
		binary.LittleEndian.PutUint64(pubKey, uint64(i))
		validators = append(validators, &ethpb.Validator{
			PublicKey:             pubKey,
			WithdrawalCredentials: withdrawalCred,
			ExitEpoch:             params.BeaconConfig().FarFutureEpoch,
			EffectiveBalance:      params.BeaconConfig().MaxEffectiveBalance,
			ActivationEpoch:       0,
		})
	}

	blk := testutil.NewBeaconBlock().Block
	blockRoot, err := blk.HashTreeRoot()
	require.NoError(t, err)

	s, err := testutil.NewBeaconState()
	require.NoError(t, err)
	require.NoError(t, s.SetValidators(validators))
	require.NoError(t, db.SaveState(ctx, s, blockRoot))
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, blockRoot))

	bs := &Server{
		BeaconDB: db,
		HeadFetcher: &mock.ChainService{
			State: s,
		},
		FinalizationFetcher: &mock.ChainService{
			FinalizedCheckPoint: &ethpb.Checkpoint{
				Epoch: 0,
			},
		},
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}

	wanted := fmt.Sprintf("page start %d >= list %d", 500, count)
	_, err = bs.ListValidatorAssignments(context.Background(), &ethpb.ListValidatorAssignmentsRequest{
		PageToken:   strconv.Itoa(2),
		QueryFilter: &ethpb.ListValidatorAssignmentsRequest_Genesis{Genesis: true},
	})
	assert.ErrorContains(t, wanted, err)
}

func TestServer_ListAssignments_Pagination_ExceedsMaxPageSize(t *testing.T) {
	bs := &Server{}
	exceedsMax := int32(cmd.Get().MaxRPCPageSize + 1)

	wanted := fmt.Sprintf("Requested page size %d can not be greater than max size %d", exceedsMax, cmd.Get().MaxRPCPageSize)
	req := &ethpb.ListValidatorAssignmentsRequest{
		PageToken: strconv.Itoa(0),
		PageSize:  exceedsMax,
	}
	_, err := bs.ListValidatorAssignments(context.Background(), req)
	assert.ErrorContains(t, wanted, err)
}

func TestServer_ListAssignments_Pagination_DefaultPageSize_NoArchive(t *testing.T) {
	helpers.ClearCache()
	db := dbTest.SetupDB(t)
	ctx := context.Background()
	count := 500
	validators := make([]*ethpb.Validator, 0, count)
	for i := 0; i < count; i++ {
		pubKey := make([]byte, params.BeaconConfig().BLSPubkeyLength)
		withdrawalCred := make([]byte, 32)
		binary.LittleEndian.PutUint64(pubKey, uint64(i))
		// Mark the validators with index divisible by 3 inactive.
		if i%3 == 0 {
			validators = append(validators, &ethpb.Validator{
				PublicKey:             pubKey,
				WithdrawalCredentials: withdrawalCred,
				ExitEpoch:             0,
				ActivationEpoch:       0,
				EffectiveBalance:      params.BeaconConfig().MaxEffectiveBalance,
			})
		} else {
			validators = append(validators, &ethpb.Validator{
				PublicKey:             pubKey,
				WithdrawalCredentials: withdrawalCred,
				ExitEpoch:             params.BeaconConfig().FarFutureEpoch,
				EffectiveBalance:      params.BeaconConfig().MaxEffectiveBalance,
				ActivationEpoch:       0,
			})
		}
	}

	blk := testutil.NewBeaconBlock().Block
	blockRoot, err := blk.HashTreeRoot()
	require.NoError(t, err)

	s, err := testutil.NewBeaconState()
	require.NoError(t, err)
	require.NoError(t, s.SetValidators(validators))
	require.NoError(t, db.SaveState(ctx, s, blockRoot))
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, blockRoot))

	bs := &Server{
		BeaconDB: db,
		HeadFetcher: &mock.ChainService{
			State: s,
		},
		FinalizationFetcher: &mock.ChainService{
			FinalizedCheckPoint: &ethpb.Checkpoint{
				Epoch: 0,
			},
		},
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}

	res, err := bs.ListValidatorAssignments(context.Background(), &ethpb.ListValidatorAssignmentsRequest{
		QueryFilter: &ethpb.ListValidatorAssignmentsRequest_Genesis{Genesis: true},
	})
	require.NoError(t, err)

	// Construct the wanted assignments.
	var wanted []*ethpb.ValidatorAssignments_CommitteeAssignment

	activeIndices, err := helpers.ActiveValidatorIndices(s, 0)
	require.NoError(t, err)
	committeeAssignments, proposerIndexToSlots, err := helpers.CommitteeAssignments(s, 0)
	require.NoError(t, err)
	for _, index := range activeIndices[0:params.BeaconConfig().DefaultPageSize] {
		val, err := s.ValidatorAtIndex(index)
		require.NoError(t, err)
		wanted = append(wanted, &ethpb.ValidatorAssignments_CommitteeAssignment{
			BeaconCommittees: committeeAssignments[index].Committee,
			CommitteeIndex:   committeeAssignments[index].CommitteeIndex,
			AttesterSlot:     committeeAssignments[index].AttesterSlot,
			ProposerSlots:    proposerIndexToSlots[index],
			PublicKey:        val.PublicKey,
			ValidatorIndex:   index,
		})
	}
	assert.DeepEqual(t, wanted, res.Assignments, "Did not receive wanted assignments")
}

func TestServer_ListAssignments_FilterPubkeysIndices_NoPagination(t *testing.T) {
	helpers.ClearCache()
	db := dbTest.SetupDB(t)

	ctx := context.Background()
	count := 100
	validators := make([]*ethpb.Validator, 0, count)
	withdrawCreds := make([]byte, 32)
	for i := 0; i < count; i++ {
		pubKey := make([]byte, params.BeaconConfig().BLSPubkeyLength)
		binary.LittleEndian.PutUint64(pubKey, uint64(i))
		val := &ethpb.Validator{
			PublicKey:             pubKey,
			WithdrawalCredentials: withdrawCreds,
			ExitEpoch:             params.BeaconConfig().FarFutureEpoch,
		}
		validators = append(validators, val)
	}

	blk := testutil.NewBeaconBlock().Block
	blockRoot, err := blk.HashTreeRoot()
	require.NoError(t, err)
	s, err := testutil.NewBeaconState()
	require.NoError(t, err)
	require.NoError(t, s.SetValidators(validators))
	require.NoError(t, db.SaveState(ctx, s, blockRoot))
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, blockRoot))

	bs := &Server{
		BeaconDB: db,
		FinalizationFetcher: &mock.ChainService{
			FinalizedCheckPoint: &ethpb.Checkpoint{
				Epoch: 0,
			},
		},
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}

	pubKey1 := make([]byte, params.BeaconConfig().BLSPubkeyLength)
	binary.LittleEndian.PutUint64(pubKey1, 1)
	pubKey2 := make([]byte, params.BeaconConfig().BLSPubkeyLength)
	binary.LittleEndian.PutUint64(pubKey2, 2)
	req := &ethpb.ListValidatorAssignmentsRequest{PublicKeys: [][]byte{pubKey1, pubKey2}, Indices: []types.ValidatorIndex{2, 3}}
	res, err := bs.ListValidatorAssignments(context.Background(), req)
	require.NoError(t, err)

	// Construct the wanted assignments.
	var wanted []*ethpb.ValidatorAssignments_CommitteeAssignment

	activeIndices, err := helpers.ActiveValidatorIndices(s, 0)
	require.NoError(t, err)
	committeeAssignments, proposerIndexToSlots, err := helpers.CommitteeAssignments(s, 0)
	require.NoError(t, err)
	for _, index := range activeIndices[1:4] {
		val, err := s.ValidatorAtIndex(index)
		require.NoError(t, err)
		wanted = append(wanted, &ethpb.ValidatorAssignments_CommitteeAssignment{
			BeaconCommittees: committeeAssignments[index].Committee,
			CommitteeIndex:   committeeAssignments[index].CommitteeIndex,
			AttesterSlot:     committeeAssignments[index].AttesterSlot,
			ProposerSlots:    proposerIndexToSlots[index],
			PublicKey:        val.PublicKey,
			ValidatorIndex:   index,
		})
	}

	assert.DeepEqual(t, wanted, res.Assignments, "Did not receive wanted assignments")
}

func TestServer_ListAssignments_CanFilterPubkeysIndices_WithPagination(t *testing.T) {
	helpers.ClearCache()
	db := dbTest.SetupDB(t)
	ctx := context.Background()
	count := 100
	validators := make([]*ethpb.Validator, 0, count)
	withdrawCred := make([]byte, 32)
	for i := 0; i < count; i++ {
		pubKey := make([]byte, params.BeaconConfig().BLSPubkeyLength)
		binary.LittleEndian.PutUint64(pubKey, uint64(i))
		val := &ethpb.Validator{
			PublicKey:             pubKey,
			WithdrawalCredentials: withdrawCred,
			ExitEpoch:             params.BeaconConfig().FarFutureEpoch,
		}
		validators = append(validators, val)
	}

	blk := testutil.NewBeaconBlock().Block
	blockRoot, err := blk.HashTreeRoot()
	require.NoError(t, err)
	s, err := testutil.NewBeaconState()
	require.NoError(t, err)
	require.NoError(t, s.SetValidators(validators))
	require.NoError(t, db.SaveState(ctx, s, blockRoot))
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, blockRoot))

	bs := &Server{
		BeaconDB: db,
		FinalizationFetcher: &mock.ChainService{
			FinalizedCheckPoint: &ethpb.Checkpoint{
				Epoch: 0,
			},
		},
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}

	req := &ethpb.ListValidatorAssignmentsRequest{Indices: []types.ValidatorIndex{1, 2, 3, 4, 5, 6}, PageSize: 2, PageToken: "1"}
	res, err := bs.ListValidatorAssignments(context.Background(), req)
	require.NoError(t, err)

	// Construct the wanted assignments.
	var assignments []*ethpb.ValidatorAssignments_CommitteeAssignment

	activeIndices, err := helpers.ActiveValidatorIndices(s, 0)
	require.NoError(t, err)
	committeeAssignments, proposerIndexToSlots, err := helpers.CommitteeAssignments(s, 0)
	require.NoError(t, err)
	for _, index := range activeIndices[3:5] {
		val, err := s.ValidatorAtIndex(index)
		require.NoError(t, err)
		assignments = append(assignments, &ethpb.ValidatorAssignments_CommitteeAssignment{
			BeaconCommittees: committeeAssignments[index].Committee,
			CommitteeIndex:   committeeAssignments[index].CommitteeIndex,
			AttesterSlot:     committeeAssignments[index].AttesterSlot,
			ProposerSlots:    proposerIndexToSlots[index],
			PublicKey:        val.PublicKey,
			ValidatorIndex:   index,
		})
	}

	wantedRes := &ethpb.ValidatorAssignments{
		Assignments:   assignments,
		TotalSize:     int32(len(req.Indices)),
		NextPageToken: "2",
	}

	assert.DeepEqual(t, wantedRes, res, "Did not get wanted assignments")

	// Test the wrap around scenario.
	assignments = nil
	req = &ethpb.ListValidatorAssignmentsRequest{Indices: []types.ValidatorIndex{1, 2, 3, 4, 5, 6}, PageSize: 5, PageToken: "1"}
	res, err = bs.ListValidatorAssignments(context.Background(), req)
	require.NoError(t, err)
	cAssignments, proposerIndexToSlots, err := helpers.CommitteeAssignments(s, 0)
	require.NoError(t, err)
	for _, index := range activeIndices[6:7] {
		val, err := s.ValidatorAtIndex(index)
		require.NoError(t, err)
		assignments = append(assignments, &ethpb.ValidatorAssignments_CommitteeAssignment{
			BeaconCommittees: cAssignments[index].Committee,
			CommitteeIndex:   cAssignments[index].CommitteeIndex,
			AttesterSlot:     cAssignments[index].AttesterSlot,
			ProposerSlots:    proposerIndexToSlots[index],
			PublicKey:        val.PublicKey,
			ValidatorIndex:   index,
		})
	}

	wantedRes = &ethpb.ValidatorAssignments{
		Assignments:   assignments,
		TotalSize:     int32(len(req.Indices)),
		NextPageToken: "",
	}

	assert.DeepEqual(t, wantedRes, res, "Did not receive wanted assignments")
}

func TestServer_NextEpochProposerList(t *testing.T) {
	helpers.ClearCache()
	db := dbTest.SetupDB(t)
	ctx := context.Background()
	count := 10000
	validators := make([]*ethpb.Validator, 0, count)
	withdrawCred := make([]byte, 32)
	for i := 0; i < count; i++ {
		pubKey := make([]byte, params.BeaconConfig().BLSPubkeyLength)
		binary.LittleEndian.PutUint64(pubKey, uint64(i))
		val := &ethpb.Validator{
			PublicKey:             pubKey,
			WithdrawalCredentials: withdrawCred,
			ExitEpoch:             params.BeaconConfig().FarFutureEpoch,
		}
		validators = append(validators, val)
	}

	config := params.BeaconConfig().Copy()
	oldConfig := config.Copy()
	config.SlotsPerEpoch = 32
	params.OverrideBeaconConfig(config)

	defer func() {
		params.OverrideBeaconConfig(oldConfig)
	}()

	blk := testutil.NewBeaconBlock().Block
	blockRoot, err := blk.HashTreeRoot()
	require.NoError(t, err)
	s, err := testutil.NewBeaconState()
	require.NoError(t, err)
	require.NoError(t, s.SetValidators(validators))
	require.NoError(t, db.SaveState(ctx, s, blockRoot))
	require.NoError(t, db.SaveGenesisBlockRoot(ctx, blockRoot))

	bs := &Server{
		BeaconDB: db,
		FinalizationFetcher: &mock.ChainService{
			FinalizedCheckPoint: &ethpb.Checkpoint{
				Epoch: 0,
			},
		},
		GenesisTimeFetcher: &mock.ChainService{},
		StateGen:           stategen.New(db),
	}

	t.Run("should return 32 proposers for epoch 0", func(t *testing.T) {
		ctx := context.Background()
		assignments, err := bs.GetProposerListForEpoch(ctx, types.Epoch(0))
		require.NoError(t, err)
		assert.Equal(t, types.Epoch(0), assignments.Epoch)
		// This is genesis epoch, so there will be 31 slots instead of 32
		require.Equal(t, int(config.SlotsPerEpoch)-1, len(assignments.Assignments))
	})

	t.Run("should return 32 proposer for each epoch", func(t *testing.T) {
		maxEpochs := 4
		// Go through 4 epochs
		count := types.Slot(maxEpochs) * config.SlotsPerEpoch
		// Should return the proper genesis block if it exists.
		parentRoot := [32]byte{1, 2, 3}
		blk := testutil.NewBeaconBlock()
		blk.Block.ParentRoot = parentRoot[:]
		root, err := blk.Block.HashTreeRoot()
		require.NoError(t, err)
		require.NoError(t, db.SaveBlock(ctx, blk))
		require.NoError(t, db.SaveGenesisBlockRoot(ctx, root))

		parentRoot = root

		blks := make([]*ethpb.SignedBeaconBlock, count)
		for i := types.Slot(0); i < count; i++ {
			b := testutil.NewBeaconBlock()
			b.Block.Slot = i
			b.Block.ParentRoot = parentRoot[:]
			blks[i] = b
			currentRoot, err := b.Block.HashTreeRoot()
			require.NoError(t, err)
			parentRoot = currentRoot
		}
		require.NoError(t, db.SaveBlocks(ctx, blks))
		ctx := context.Background()

		// Start from epoch 1
		for index := 1; index < maxEpochs; index++ {
			assignments, err := bs.GetProposerListForEpoch(ctx, types.Epoch(index))
			require.NoError(t, err)
			assert.Equal(t, types.Epoch(index), assignments.Epoch)
			require.Equal(t, config.SlotsPerEpoch, len(assignments.Assignments))
		}
	})
}

func TestServer_SubscribeMinimalConsensusInfo(t *testing.T) {
	type MinimalEpochConsensusInfo struct {
		// Epoch number
		Epoch            uint64        `json:"epoch"`
		// Validators public key list for specific epoch
		ValidatorList    [32]string    `json:"validatorList"`
		EpochStartTime   uint64        `json:"epochTimeStart"`
		SlotTimeDuration time.Duration `json:"slotTimeDuration"`
	}

	var (
		minimalConsensusInfo MinimalEpochConsensusInfo
		done sync.WaitGroup
		ch   = make(chan int)
		sub1 = feed.Subscribe(ch)
		sub2 = feed.Subscribe(ch)
		_    = feed.Subscribe(ch)
	)
	expectSends := func(value, n int) {
		if nsent := feed.Send(value); nsent != n {
			t.Errorf("send delivered %d times, want %d", nsent, n)
		}
		done.Done()
	}
	expectRecv := func(wantValue, n int) {
		for i := 0; i < n; i++ {
			if v := <-ch; v != wantValue {
				t.Errorf("received %d, want %d", v, wantValue)
			}
		}
	}

	done.Add(1)
	go expectSends(1, 3)
	expectRecv(1, 3)
	done.Wait()

	sub1.Unsubscribe()

	done.Add(1)
	go expectSends(2, 2)
	expectRecv(2, 2)
	done.Wait()

	sub2.Unsubscribe()

	done.Add(1)
	go expectSends(3, 1)
	expectRecv(3, 1)
	done.Wait()
}
