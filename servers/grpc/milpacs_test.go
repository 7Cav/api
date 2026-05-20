package grpc

import (
	"errors"
	"testing"

	"github.com/7cav/api/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

func TestGetProfile_ByUsername_NotFound(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfilesByUsername: func(string) ([]*proto.Profile, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}}
	_, err := svc.GetProfile(withMilpacsKey("read"), &proto.ProfileRequest{Username: "ghost"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetProfile_ByUsername_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfilesByUsername: func(string) ([]*proto.Profile, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetProfile(withMilpacsKey("read"), &proto.ProfileRequest{Username: "anyone"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetProfile_ByUserID_NotFound(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfilesById: func(...uint64) ([]*proto.Profile, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}}
	_, err := svc.GetProfile(withMilpacsKey("read"), &proto.ProfileRequest{UserId: 99999})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetProfile_ByUserID_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfilesById: func(...uint64) ([]*proto.Profile, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetProfile(withMilpacsKey("read"), &proto.ProfileRequest{UserId: 42})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetUserViaKeycloakId_NotFound(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByKeycloakID: func(string) (*proto.Profile, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}}
	_, err := svc.GetUserViaKeycloakId(withMilpacsKey("read"), &proto.KeycloakIdRequest{KeycloakId: "ghost-uuid"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetUserViaKeycloakId_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByKeycloakID: func(string) (*proto.Profile, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetUserViaKeycloakId(withMilpacsKey("read"), &proto.KeycloakIdRequest{KeycloakId: "any"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetUserViaDiscordId_NotFound(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByDiscordID: func(string) (*proto.Profile, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}}
	_, err := svc.GetUserViaDiscordId(withMilpacsKey("read"), &proto.DiscordIdRequest{DiscordId: "404"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetUserViaDiscordId_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByDiscordID: func(string) (*proto.Profile, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetUserViaDiscordId(withMilpacsKey("read"), &proto.DiscordIdRequest{DiscordId: "any"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetGamertagProfile_NotFound(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByGamertag: func(string) (*proto.Profile, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}}
	_, err := svc.GetGamertagProfile(withMilpacsKey("read"), &proto.GamertagRequest{Gamertag: "Nobody#0001"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetGamertagProfile_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findProfileByGamertag: func(string) (*proto.Profile, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetGamertagProfile(withMilpacsKey("read"), &proto.GamertagRequest{Gamertag: "any"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetRoster_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findRosterByType: func(proto.RosterType) (*proto.Roster, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetRoster(withMilpacsKey("read"), &proto.RosterRequest{Roster: proto.RosterType_ROSTER_TYPE_COMBAT})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetLiteRoster_DatastoreError(t *testing.T) {
	svc := &MilpacsService{Datastore: &fakeDatastore{
		findLiteRosterByType: func(proto.RosterType) (*proto.LiteRoster, error) {
			return nil, errors.New("boom")
		},
	}}
	_, err := svc.GetLiteRoster(withMilpacsKey("read"), &proto.RosterRequest{Roster: proto.RosterType_ROSTER_TYPE_COMBAT})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
