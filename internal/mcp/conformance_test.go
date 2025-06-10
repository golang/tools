// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.24 && goexperiment.synctest

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "if set, update conformance test data")

// A conformance test checks JSON-level conformance of a test server or client.
// This allows us to confirm that we can handle the input or output of other
// SDKs, even if they behave differently at the JSON level (for example, have
// different behavior with respect to optional fields).
//
// The client and server fields hold an encoded sequence of JSON-RPC messages.
//
// For server tests, the client messages are a sequence of messages to be sent
// from the (synthetic) client and the server messages are the expected
// messages to be received from the real server.
//
// For client tests, it's the other way around: server messages are synthetic,
// and client messages are expected from the real client.
//
// Conformance tests are loaded from txtar-encoded testdata files. Run the test
// with -update to have the test runner update the expected output, which may
// be client or server depending on the perspective of the test.
type conformanceTest struct {
	name                      string             // test name
	path                      string             // path to test file
	archive                   *txtar.Archive     // raw archive, for updating
	tools, prompts, resources []string           // named features to include
	client                    []jsonrpc2.Message // client messages
	server                    []jsonrpc2.Message // server messages
}

// TODO(rfindley): add client conformance tests.

func TestServerConformance(t *testing.T) {
	var tests []*conformanceTest
	dir := filepath.Join("testdata", "conformance", "server")
	if err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".txtar") {
			test, err := loadConformanceTest(dir, path)
			if err != nil {
				return fmt.Errorf("%s: %v", path, err)
			}
			tests = append(tests, test)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// We use synctest here because in general, there is no way to know when the
			// server is done processing any notifications. As long as our server doesn't
			// do background work, synctest provides an easy way for us to detect when the
			// server is done processing.
			//
			// By comparison, gopls has a complicated framework based on progress
			// reporting and careful accounting to detect when all 'expected' work
			// on the server is complete.
			synctest.Run(func() { runServerTest(t, test) })

			// TODO: in 1.25, use the following instead:
			// synctest.Test(t, func(t *testing.T) {
			// 	runServerTest(t, test)
			// })
		})
	}
}

// runServerTest runs the server conformance test.
// It must be executed in a synctest bubble.
func runServerTest(t *testing.T, test *conformanceTest) {
	ctx := t.Context()
	// Construct the server based on features listed in the test.
	s := NewServer("testServer", "v1.0.0", nil)
	add(tools, s.AddTools, test.tools...)
	add(prompts, s.AddPrompts, test.prompts...)
	add(resources, s.AddResources, test.resources...)

	// Connect the server, and connect the client stream,
	// but don't connect an actual client.
	cTransport, sTransport := NewInMemoryTransports()
	ss, err := s.Connect(ctx, sTransport)
	if err != nil {
		t.Fatal(err)
	}
	cStream, err := cTransport.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	writeMsg := func(msg jsonrpc2.Message) {
		if err := cStream.Write(ctx, msg); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	var (
		serverMessages []jsonrpc2.Message
		outRequests    []*jsonrpc2.Request
		outResponses   []*jsonrpc2.Response
	)

	// Separate client requests and responses; we use them differently.
	for _, msg := range test.client {
		switch msg := msg.(type) {
		case *jsonrpc2.Request:
			outRequests = append(outRequests, msg)
		case *jsonrpc2.Response:
			outResponses = append(outResponses, msg)
		default:
			t.Fatalf("bad message type %T", msg)
		}
	}

	// nextResponse handles incoming requests and notifications, and returns the
	// next incoming response.
	nextResponse := func() (*jsonrpc2.Response, error, bool) {
		for {
			msg, err := cStream.Read(ctx)
			if err != nil {
				// TODO(rfindley): we don't document (or want to document) that the in
				// memory transports use a net.Pipe. How can users detect this failure?
				// Should we promote it to EOF?
				if errors.Is(err, io.ErrClosedPipe) {
					err = nil
				}
				return nil, err, false
			}
			serverMessages = append(serverMessages, msg)
			if req, ok := msg.(*jsonrpc2.Request); ok && req.ID.IsValid() {
				// Pair up the next outgoing response with this request.
				// We assume requests arrive in the same order every time.
				if len(outResponses) == 0 {
					t.Fatalf("no outgoing response for request %v", req)
				}
				outResponses[0].ID = req.ID
				writeMsg(outResponses[0])
				outResponses = outResponses[1:]
				continue
			}
			return msg.(*jsonrpc2.Response), nil, true
		}
	}

	// Synthetic peer interacts with real peer.
	for _, req := range outRequests {
		writeMsg(req)
		if req.ID.IsValid() {
			// A request (as opposed to a notification). Wait for the response.
			res, err, ok := nextResponse()
			if err != nil {
				t.Fatalf("reading server messages failed: %v", err)
			}
			if !ok {
				t.Fatalf("missing response for request %v", req)
			}
			if res.ID != req.ID {
				t.Fatalf("out-of-order response %v to request %v", req, res)
			}
		}
	}
	// There might be more notifications or requests, but there shouldn't be more
	// responses.
	// Run this in a goroutine so the current thread can wait for it.
	var extra *jsonrpc2.Response
	go func() {
		extra, err, _ = nextResponse()
	}()
	// Before closing the stream, wait for all messages to be processed.
	synctest.Wait()
	if err != nil {
		t.Fatalf("reading server messages failedd: %v", err)
	}
	if extra != nil {
		t.Fatalf("got extra response: %v", extra)
	}
	if err := cStream.Close(); err != nil {
		t.Fatalf("Stream.Close failed: %v", err)
	}
	ss.Wait()

	// Handle server output. If -update is set, write the 'server' file.
	// Otherwise, compare with expected.
	if *update {
		arch := &txtar.Archive{
			Comment: test.archive.Comment,
		}
		var buf bytes.Buffer
		for _, msg := range serverMessages {
			data, err := jsonrpc2.EncodeIndent(msg, "", "\t")
			if err != nil {
				t.Fatalf("jsonrpc2.EncodeIndent failed: %v", err)
			}
			buf.Write(data)
			buf.WriteByte('\n')
		}
		serverFile := txtar.File{Name: "server", Data: buf.Bytes()}
		seenServer := false // replace or append the 'server' file
		for _, f := range test.archive.Files {
			if f.Name == "server" {
				seenServer = true
				arch.Files = append(arch.Files, serverFile)
			} else {
				arch.Files = append(arch.Files, f)
			}
		}
		if !seenServer {
			arch.Files = append(arch.Files, serverFile)
		}
		if err := os.WriteFile(test.path, txtar.Format(arch), 0o666); err != nil {
			t.Fatalf("os.WriteFile(%q) failed: %v", test.path, err)
		}
	} else {
		// jsonrpc2.Messages are not comparable, so we instead compare lines of JSON.
		transform := cmpopts.AcyclicTransformer("toJSON", func(msg jsonrpc2.Message) []string {
			encoded, err := jsonrpc2.EncodeIndent(msg, "", "\t")
			if err != nil {
				t.Fatal(err)
			}
			return strings.Split(string(encoded), "\n")
		})
		if diff := cmp.Diff(test.server, serverMessages, transform); diff != "" {
			t.Errorf("Mismatching server messages (-want +got):\n%s", diff)
		}
	}
}

// loadConformanceTest loads one conformance test from the given path contained
// in the root dir.
func loadConformanceTest(dir, path string) (*conformanceTest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	test := &conformanceTest{
		name:    strings.TrimPrefix(path, dir+string(filepath.Separator)),
		path:    path,
		archive: txtar.Parse(content),
	}
	if len(test.archive.Files) == 0 {
		return nil, fmt.Errorf("txtar archive %q has no '-- filename --' sections", path)
	}

	// decodeMessages loads JSON-RPC messages from the archive file.
	decodeMessages := func(data []byte) ([]jsonrpc2.Message, error) {
		dec := json.NewDecoder(bytes.NewReader(data))
		var res []jsonrpc2.Message
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, err
			}
			m, err := jsonrpc2.DecodeMessage(raw)
			if err != nil {
				return nil, err
			}
			res = append(res, m)
		}
		return res, nil
	}
	// loadFeatures loads lists of named features from the archive file.
	loadFeatures := func(data []byte) []string {
		var feats []string
		for line := range strings.Lines(string(data)) {
			if f := strings.TrimSpace(line); f != "" {
				feats = append(feats, f)
			}
		}
		return feats
	}

	seen := make(map[string]bool) // catch accidentally duplicate files
	for _, f := range test.archive.Files {
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate file name %q", f.Name)
		}
		seen[f.Name] = true
		switch f.Name {
		case "tools":
			test.tools = loadFeatures(f.Data)
		case "prompts":
			test.prompts = loadFeatures(f.Data)
		case "resources":
			test.resources = loadFeatures(f.Data)
		case "client":
			test.client, err = decodeMessages(f.Data)
			if err != nil {
				return nil, fmt.Errorf("txtar archive %q contains bad -- client -- section: %v", path, err)
			}
		case "server":
			test.server, err = decodeMessages(f.Data)
			if err != nil {
				return nil, fmt.Errorf("txtar archive %q contains bad -- server -- section: %v", path, err)
			}
		default:
			return nil, fmt.Errorf("txtar archive %q contains unexpected file %q", path, f.Name)
		}
	}

	return test, nil
}
