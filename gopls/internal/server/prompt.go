// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/telemetry"
	"golang.org/x/telemetry/counter"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// promptTimeout is the amount of time we wait for an ongoing prompt before
// prompting again. This gives the user time to reply. However, at some point
// we must assume that the client is not displaying the prompt, the user is
// ignoring it, or the prompt has been disrupted in some way (e.g. by a gopls
// crash).
const promptTimeout = 24 * time.Hour

// gracePeriod is the amount of time we wait before sufficient telemetry data
// is accumulated in the local directory, so users can have time to review
// what kind of information will be collected and uploaded when prompting starts.
const gracePeriod = 7 * 24 * time.Hour

// samplesPerMille is the prompt probability.
// Token is an integer between [1, 1000] and is assigned when maybePromptForTelemetry
// is called first time. Only the user with a token ∈ [1, samplesPerMille]
// will be considered for prompting.
const samplesPerMille = 10 // 1% sample rate

// The following constants are used for testing telemetry integration.
const (
	TelemetryPromptWorkTitle    = "Checking telemetry prompt"     // progress notification title, for awaiting in tests
	GoplsConfigDirEnvvar        = "GOPLS_CONFIG_DIR"              // overridden for testing
	FakeTelemetryModefileEnvvar = "GOPLS_FAKE_TELEMETRY_MODEFILE" // overridden for testing
	FakeSamplesPerMille         = "GOPLS_FAKE_SAMPLES_PER_MILLE"  // overridden for testing
	TelemetryYes                = "Yes, I'd like to help."
	TelemetryNo                 = "No, thanks."
)

// The following environment variables may be set by the client.
// Exported for testing telemetry integration.
const (
	GoTelemetryGoplsClientStartTimeEnvvar = "GOTELEMETRY_GOPLS_CLIENT_START_TIME" // telemetry start time recored in client
	GoTelemetryGoplsClientTokenEnvvar     = "GOTELEMETRY_GOPLS_CLIENT_TOKEN"      // sampling token
)

// getenv returns the effective environment variable value for the provided
// key, looking up the key in the session environment before falling back on
// the process environment.
func (s *server) getenv(key string) string {
	if v, ok := s.Options().Env[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// telemetryMode returns the current effective telemetry mode.
// By default this is x/telemetry.Mode(), but it may be overridden for tests.
func (s *server) telemetryMode() string {
	if fake := s.getenv(FakeTelemetryModefileEnvvar); fake != "" {
		if data, err := os.ReadFile(fake); err == nil {
			return string(data)
		}
		return "local"
	}
	return telemetry.Mode()
}

// setTelemetryMode sets the current telemetry mode.
// By default this calls x/telemetry.SetMode, but it may be overridden for
// tests.
func (s *server) setTelemetryMode(mode string) error {
	if fake := s.getenv(FakeTelemetryModefileEnvvar); fake != "" {
		return os.WriteFile(fake, []byte(mode), 0666)
	}
	return telemetry.SetMode(mode)
}

// maybePromptForTelemetry checks for the right conditions, and then prompts
// the user to ask if they want to enable Go telemetry uploading. If the user
// responds 'Yes', the telemetry mode is set to "on".
//
// The actual conditions for prompting are defensive, erring on the side of not
// prompting.
// If enabled is false, this will not prompt the user in any condition,
// but will send work progress reports to help testing.
func (s *server) maybePromptForTelemetry(ctx context.Context, enabled bool) {
	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, TelemetryPromptWorkTitle, "Checking if gopls should prompt about telemetry...", nil, nil)
		defer work.End(ctx, "Done.")
	}

	errorf := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		event.Error(ctx, "telemetry prompt failed", err)
	}

	// Only prompt if we can read/write the prompt config file.
	configDir := s.getenv(GoplsConfigDirEnvvar) // set for testing
	if configDir == "" && testing.Testing() {
		// Unless tests set GoplsConfigDirEnvvar, the prompt is a no op.
		// We don't want tests to interact with os.UserConfigDir().
		return
	}
	if configDir == "" {
		userDir, err := os.UserConfigDir()
		if err != nil {
			errorf("unable to determine user config dir: %v", err)
			return
		}
		configDir = filepath.Join(userDir, "gopls")
	}

	// Read the current prompt file.

	var (
		promptDir  = filepath.Join(configDir, "prompt")    // prompt configuration directory
		promptFile = filepath.Join(promptDir, "telemetry") // telemetry prompt file
	)

	// prompt states, stored in the prompt file
	const (
		pUnknown  = ""        // first time
		pNotReady = "-"       // user is not asked yet (either not sampled or not past the grace period)
		pYes      = "yes"     // user said yes
		pNo       = "no"      // user said no
		pPending  = "pending" // current prompt is still pending
		pFailed   = "failed"  // prompt was asked but failed
	)
	validStates := map[string]bool{
		pNotReady: true,
		pYes:      true,
		pNo:       true,
		pPending:  true,
		pFailed:   true,
	}

	// Parse the current prompt file.
	var (
		state    = pUnknown
		attempts = 0 // number of times we've asked already

		// the followings are recorded after gopls v0.17+.
		token        = 0   // valid token is [1, 1000]
		creationTime int64 // unix time sec
	)
	if content, err := os.ReadFile(promptFile); err == nil {
		if n, _ := fmt.Sscanf(string(content), "%s %d %d %d", &state, &attempts, &creationTime, &token); (n == 2 || n == 4) && validStates[state] {
			// successfully parsed!
			//  ~ v0.16: must have only two fields, state and attempts.
			//  v0.17 ~: must have all four fields.
		} else {
			state, attempts, creationTime, token = pUnknown, 0, 0, 0
			// TODO(hyangah): why do we want to present this as an error to user?
			errorf("malformed prompt result %q", string(content))
		}
	} else if !os.IsNotExist(err) {
		errorf("reading prompt file: %v", err)
		// Something went wrong. Since we don't know how many times we've asked the
		// prompt, err on the side of not asking.
		//
		// But record this in telemetry, in case some users enable telemetry by
		// other means.
		counter.New("gopls/telemetryprompt/corrupted").Inc()
		return
	}

	counter.New(fmt.Sprintf("gopls/telemetryprompt/attempts:%d", attempts)).Inc()

	// Check terminal conditions.

	if state == pYes {
		// Prompt has been accepted.
		//
		// We record this counter for every gopls session, rather than when the
		// prompt actually accepted below, because if we only recorded it in the
		// counter file at the time telemetry is enabled, we'd never upload it,
		// because we exclude any counter files that overlap with a time period
		// that has telemetry uploading is disabled.
		counter.New("gopls/telemetryprompt/accepted").Inc()
		return
	}
	if state == pNo {
		// Prompt has been declined. In most cases, this means we'll never see the
		// counter below, but it's possible that the user may enable telemetry by
		// other means later on. If we see a significant number of users that have
		// accepted telemetry but declined the prompt, it may be an indication that
		// the prompt is not working well.
		counter.New("gopls/telemetryprompt/declined").Inc()
		return
	}
	if attempts >= 5 { // pPending or pFailed
		// We've tried asking enough; give up. Record that the prompt expired, in
		// case the user decides to enable telemetry by other means later on.
		// (see also the pNo case).
		counter.New("gopls/telemetryprompt/expired").Inc()
		return
	}

	// We only check enabled after (1) the work progress is started, and (2) the
	// prompt file has been read. (1) is for testing purposes, and (2) is so that
	// we record the "gopls/telemetryprompt/accepted" counter for every session.
	if !enabled {
		return // prompt is disabled
	}

	if s.telemetryMode() == "on" || s.telemetryMode() == "off" {
		// Telemetry is already on or explicitly off -- nothing to ask about.
		return
	}

	// Transition: pUnknown -> pNotReady
	if state == pUnknown {
		// First time; we need to make the prompt dir.
		if err := os.MkdirAll(promptDir, 0777); err != nil {
			errorf("creating prompt dir: %v", err)
			return
		}
		state = pNotReady
	}

	// Correct missing values.
	if creationTime == 0 {
		creationTime = time.Now().Unix()
		if v := s.getenv(GoTelemetryGoplsClientStartTimeEnvvar); v != "" {
			if sec, err := strconv.ParseInt(v, 10, 64); err == nil && sec > 0 {
				creationTime = sec
			}
		}
	}
	if token == 0 {
		token = rand.Intn(1000) + 1
		if v := s.getenv(GoTelemetryGoplsClientTokenEnvvar); v != "" {
			if tok, err := strconv.Atoi(v); err == nil && 1 <= tok && tok <= 1000 {
				token = tok
			}
		}
	}

	// Transition: pNotReady -> pPending if sampled
	if state == pNotReady {
		threshold := samplesPerMille
		if v := s.getenv(FakeSamplesPerMille); v != "" {
			if t, err := strconv.Atoi(v); err == nil {
				threshold = t
			}
		}
		if token <= threshold && time.Now().Unix()-creationTime > gracePeriod.Milliseconds()/1000 {
			state = pPending
		}
	}

	// Acquire the lock and write the updated state to the prompt file before actually
	// prompting.
	//
	// This ensures that the prompt file is writeable, and that we increment the
	// attempt counter before we prompt, so that we don't end up in a failure
	// mode where we keep prompting and then failing to record the response.

	release, ok, err := acquireLockFile(promptFile)
	if err != nil {
		errorf("acquiring prompt: %v", err)
		return
	}
	if !ok {
		// Another process is making decision.
		return
	}
	defer release()

	if state != pNotReady { // pPending or pFailed
		attempts++
	}

	pendingContent := []byte(fmt.Sprintf("%s %d %d %d", state, attempts, creationTime, token))
	if err := os.WriteFile(promptFile, pendingContent, 0666); err != nil {
		errorf("writing pending state: %v", err)
		return
	}

	if state == pNotReady {
		return
	}

	var prompt = `Go telemetry helps us improve Go by periodically sending anonymous metrics and crash reports to the Go team. Learn more at https://go.dev/doc/telemetry.

Would you like to enable Go telemetry?
`
	if s.Options().LinkifyShowMessage {
		prompt = `Go telemetry helps us improve Go by periodically sending anonymous metrics and crash reports to the Go team. Learn more at [go.dev/doc/telemetry](https://go.dev/doc/telemetry).

Would you like to enable Go telemetry?
`
	}
	// TODO(rfindley): investigate a "tell me more" action in combination with ShowDocument.
	params := &protocol.ShowMessageRequestParams{
		Type:    protocol.Info,
		Message: prompt,
		Actions: []protocol.MessageActionItem{
			{Title: TelemetryYes},
			{Title: TelemetryNo},
		},
	}

	item, err := s.client.ShowMessageRequest(ctx, params)
	if err != nil {
		errorf("ShowMessageRequest failed: %v", err)
		// Defensive: ensure item == nil for the logic below.
		item = nil
	}

	message := func(typ protocol.MessageType, msg string) {
		if !showMessage(ctx, s.client, typ, msg) {
			// Make sure we record that "telemetry prompt failed".
			errorf("showMessage failed: %v", err)
		}
	}

	result := pFailed
	if item == nil {
		// e.g. dialog was dismissed
		errorf("no response")
	} else {
		// Response matches MessageActionItem.Title.
		switch item.Title {
		case TelemetryYes:
			result = pYes
			if err := s.setTelemetryMode("on"); err == nil {
				message(protocol.Info, telemetryOnMessage(s.Options().LinkifyShowMessage))
			} else {
				errorf("enabling telemetry failed: %v", err)
				msg := fmt.Sprintf("Failed to enable Go telemetry: %v\nTo enable telemetry manually, please run `go run golang.org/x/telemetry/cmd/gotelemetry@latest on`", err)
				message(protocol.Error, msg)
			}

		case TelemetryNo:
			result = pNo
		default:
			errorf("unrecognized response %q", item.Title)
			message(protocol.Error, fmt.Sprintf("Unrecognized response %q", item.Title))
		}
	}
	resultContent := []byte(fmt.Sprintf("%s %d %d %d", result, attempts, creationTime, token))
	if err := os.WriteFile(promptFile, resultContent, 0666); err != nil {
		errorf("error writing result state to prompt file: %v", err)
	}
}

func telemetryOnMessage(linkify bool) string {
	format := `Thank you. Telemetry uploading is now enabled.

To disable telemetry uploading, run %s.
`
	var runCmd = "`go run golang.org/x/telemetry/cmd/gotelemetry@latest local`"
	if linkify {
		runCmd = "[gotelemetry local](https://golang.org/x/telemetry/cmd/gotelemetry)"
	}
	return fmt.Sprintf(format, runCmd)
}

// acquireLockFile attempts to "acquire a lock" for writing to path.
//
// This is achieved by creating an exclusive lock file at <path>.lock. Lock
// files expire after a period, at which point acquireLockFile will remove and
// recreate the lock file.
//
// acquireLockFile fails if path is in a directory that doesn't exist.
func acquireLockFile(path string) (func(), bool, error) {
	lockpath := path + ".lock"
	fi, err := os.Stat(lockpath)
	if err == nil {
		if time.Since(fi.ModTime()) > promptTimeout {
			_ = os.Remove(lockpath) // ignore error
		} else {
			return nil, false, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("statting lockfile: %v", err)
	}

	f, err := os.OpenFile(lockpath, os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		if os.IsExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("creating lockfile: %v", err)
	}
	fi, err = f.Stat()
	if err != nil {
		return nil, false, err
	}
	release := func() {
		_ = f.Close() // ignore error
		fi2, err := os.Stat(lockpath)
		if err == nil && os.SameFile(fi, fi2) {
			// Only clean up the lockfile if it's the same file we created.
			// Otherwise, our lock has expired and something else has the lock.
			//
			// There's a race here, in that the file could have changed since the
			// stat above; but given that we've already waited 24h this is extremely
			// unlikely, and acceptable.
			_ = os.Remove(lockpath)
		}
	}
	return release, true, nil
}
