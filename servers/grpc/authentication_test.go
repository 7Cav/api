/*
 *  Copyright (C) 2021 7Cav.us
 *  This file is part of 7Cav-API <https://github.com/7cav/api>.
 *
 *  7Cav-API is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  7Cav-API is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with 7Cav-API. If not, see <http://www.gnu.org/licenses/>.
 */

package grpc

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// reqDurationPattern extracts the duration= field from a [REQ] log line.
var reqDurationPattern = regexp.MustCompile(`duration=(\S+)`)

// extractReqDuration pulls the duration= value out of the captured log output
// and parses it, so tests assert on a real time.Duration rather than string
// shape alone.
func extractReqDuration(t *testing.T, logged string) time.Duration {
	t.Helper()
	m := reqDurationPattern.FindStringSubmatch(logged)
	require.Len(t, m, 2, "log output must carry a duration= field, got: %q", logged)
	d, err := time.ParseDuration(m[1])
	require.NoError(t, err, "duration= value must parse as a Go duration")
	return d
}

// Phase 0 measuring stick (#114): the [REQ] line must time the whole request
// including the handler, appended after the existing fields so the ad-hoc
// analytics keep parsing transport/method/peer/key_id unchanged.
func TestAuthInterceptor_ReqLineCarriesHandlerDuration(t *testing.T) {
	ds := &fakeDatastore{
		validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
			return &datastores.ApiKeyResult{KeyId: 17}, nil
		},
	}
	buf := captureInfo(t)
	interceptor := NewAuthInterceptor(ds)
	ctx := buildAuthCtx("cav7_abc", "10.0.0.5")
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}

	const handlerDelay = 15 * time.Millisecond
	resp, err := interceptor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		time.Sleep(handlerDelay)
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)

	logged := buf.String()
	// Existing fields stay, in order; duration= is appended after key_id.
	assert.Regexp(t,
		`\[REQ\] transport=grpc method=/proto\.MilpacService/GetProfile peer=10\.0\.0\.5:4242 key_id=17 duration=\S+`,
		logged)
	d := extractReqDuration(t, logged)
	assert.GreaterOrEqual(t, d, handlerDelay,
		"duration must cover the handler, not just interceptor overhead")
}

// Every [REQ] line carries duration= — including requests rejected before the
// handler runs (auth failures), where key_id stays "none".
func TestAuthInterceptor_ReqLineCarriesDurationOnAuthFailure(t *testing.T) {
	ds := &fakeDatastore{
		validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
			return nil, nil
		},
	}
	buf := captureInfo(t)
	interceptor := NewAuthInterceptor(ds)
	ctx := buildAuthCtx("cav7_badkey", "10.0.0.5")
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}

	_, err := interceptor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		t.Fatal("handler must not run on auth failure")
		return nil, nil
	})
	require.Error(t, err)

	logged := buf.String()
	assert.Regexp(t, `\[REQ\] transport=grpc method=\S+ peer=\S+ key_id=none duration=\S+`, logged)
	extractReqDuration(t, logged)
}
