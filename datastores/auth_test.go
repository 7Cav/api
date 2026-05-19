package datastores

import (
	"regexp"
	"strings"
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

func TestParseBearerToken(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		maxLen int
		want   string
	}{
		{"canonical scheme", "Bearer abc", 128, "abc"},
		{"lowercase scheme", "bearer abc", 128, "abc"},
		{"uppercase scheme", "BEARER abc", 128, "abc"},
		{"mixed-case scheme", "bEaReR abc", 128, "abc"},
		{"basic scheme rejected", "Basic abc", 128, ""},
		{"empty input", "", 128, ""},
		{"whitespace only", "   ", 128, ""},
		{"scheme with no token", "Bearer ", 128, ""},
		{"scheme with whitespace token", "Bearer    ", 128, ""},
		{"oversize token", "Bearer " + strings.Repeat("x", 200), 128, ""},
		{"max-len boundary", "Bearer " + strings.Repeat("x", 128), 128, strings.Repeat("x", 128)},
		{"one over max-len", "Bearer " + strings.Repeat("x", 129), 128, ""},
		{"outer whitespace tolerated", "  Bearer abc  ", 128, "abc"},
		{"inner extra spaces collapsed", "Bearer   abc", 128, "abc"},
		{"missing scheme separator", "Bearerabc", 128, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseBearerToken(c.raw, c.maxLen)
			assert.Equal(t, c.want, got)
		})
	}
}
