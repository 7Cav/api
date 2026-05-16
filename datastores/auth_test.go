package datastores

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newMockDS(t *testing.T) (Mysql, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)
	return Mysql{Db: gormDB}, mock, func() { sqlDB.Close() }
}

func TestValidateApiKey_ValidWithMultipleScopes(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"key_id", "user_id", "scope_name"}).
		AddRow(42, 1648, "read").
		AddRow(42, 1648, "read:tickets")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT k.key_id, k.user_id, sd.scope_name")).
		WithArgs("cav7_testtoken").
		WillReturnRows(rows)

	result, err := ds.ValidateApiKey("cav7_testtoken")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, uint(42), result.KeyId)
	assert.Equal(t, uint(1648), result.UserId)
	assert.True(t, result.HasScope("read"))
	assert.True(t, result.HasScope("read:tickets"))
	assert.False(t, result.HasScope("write:something"))
}

func TestValidateApiKey_ZeroRowsReturnsNil(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()

	rows := sqlmock.NewRows([]string{"key_id", "user_id", "scope_name"})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT k.key_id, k.user_id, sd.scope_name")).
		WithArgs("bad_token").
		WillReturnRows(rows)

	result, err := ds.ValidateApiKey("bad_token")
	require.NoError(t, err)
	assert.Nil(t, result, "no rows must return nil result (auth boundary then responds 401)")
}

func TestValidateApiKey_QueryError(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT k.key_id, k.user_id, sd.scope_name")).
		WithArgs("any_token").
		WillReturnError(assert.AnError)

	result, err := ds.ValidateApiKey("any_token")
	assert.Error(t, err)
	assert.Nil(t, result)
}
