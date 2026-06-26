package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Helper process for mocking exec.CommandContext
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	if len(args) < 3 {
		return
	}
	filePath := args[len(args)-1]

	content := []byte("updated content from mock editor\n")
	// Periodically write to the file until the process is killed by the parent context.
	// This avoids any time.Sleep dependency and ensures fsnotify catches the write event.
	for range 100 {
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "failed to write file: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOffsetToLineAndColumn(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		caret      int
		wantLine   int
		wantColumn int
	}{
		{
			name:       "start of text",
			text:       "hello\nworld",
			caret:      0,
			wantLine:   0,
			wantColumn: 0,
		},
		{
			name:       "middle of first line",
			text:       "hello\nworld",
			caret:      3,
			wantLine:   0,
			wantColumn: 3,
		},
		{
			name:       "end of first line",
			text:       "hello\nworld",
			caret:      5,
			wantLine:   0,
			wantColumn: 5,
		},
		{
			name:       "start of second line",
			text:       "hello\nworld",
			caret:      6,
			wantLine:   1,
			wantColumn: 0,
		},
		{
			name:       "middle of second line",
			text:       "hello\nworld",
			caret:      8,
			wantLine:   1,
			wantColumn: 2,
		},
		{
			name:       "out of bounds high",
			text:       "hello\nworld",
			caret:      100,
			wantLine:   1,
			wantColumn: 5,
		},
		{
			name:       "out of bounds low",
			text:       "hello\nworld",
			caret:      -5,
			wantLine:   0,
			wantColumn: 0,
		},
		{
			name:       "multiple newlines",
			text:       "\n\nhello\n",
			caret:      8,
			wantLine:   3,
			wantColumn: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &message{}
			msg.Payload.Text = tt.text
			msg.Payload.Caret = tt.caret
			line, col := offsetToLineAndColumn(msg)
			if line != tt.wantLine || col != tt.wantColumn {
				t.Errorf("offsetToLineAndColumn() = (%d, %d), want (%d, %d)", line, col, tt.wantLine, tt.wantColumn)
			}
		})
	}
}

func TestBuildArguments(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		absfn    string
		line     int
		column   int
		wantArgs []string
	}{
		{
			name:     "no placeholders",
			args:     []string{"editor"},
			absfn:    "/path/to/file.txt",
			line:     10,
			column:   5,
			wantArgs: []string{"editor", "/path/to/file.txt"},
		},
		{
			name:     "with %s placeholder",
			args:     []string{"editor", "-f", "%s"},
			absfn:    "/path/to/file.txt",
			line:     10,
			column:   5,
			wantArgs: []string{"editor", "-f", "/path/to/file.txt"},
		},
		{
			name:     "with line and column placeholders (1-indexed)",
			args:     []string{"editor", "+%l:%c", "%s"},
			absfn:    "/path/to/file.txt",
			line:     10, // 0-indexed
			column:   5,  // 0-indexed
			wantArgs: []string{"editor", "+11:6", "/path/to/file.txt"},
		},
		{
			name:     "with line and column placeholders (0-indexed)",
			args:     []string{"editor", "+%L:%C", "%s"},
			absfn:    "/path/to/file.txt",
			line:     10,
			column:   5,
			wantArgs: []string{"editor", "+10:5", "/path/to/file.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArguments(tt.args, tt.absfn, tt.line, tt.column)
			if len(got) != len(tt.wantArgs) {
				t.Fatalf("buildArguments() returned %v, want %v", got, tt.wantArgs)
			}
			for i := range got {
				if got[i] != tt.wantArgs[i] {
					t.Errorf("buildArguments()[%d] = %q, want %q", i, got[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestTmpManager(t *testing.T) {
	in := bytes.NewBuffer(nil)
	out := bytes.NewBuffer(nil)
	r := newRunner(in, out, false)

	tm, err := r.newTmpManager()
	if err != nil {
		t.Fatalf("failed to create tmpManager: %v", err)
	}
	defer os.RemoveAll(tm.tmpDir)

	msg := &message{}
	msg.Payload.ID = "test-id"
	msg.Payload.Subject = "test-subject"
	msg.Payload.Extension = "txt"
	msg.Payload.Text = "hello world"

	absfn, err := tm.create(msg)
	if err != nil {
		t.Fatalf("failed to create tmp file: %v", err)
	}

	if _, err := os.Stat(absfn); os.IsNotExist(err) {
		t.Errorf("temporary file was not created: %s", absfn)
	}

	content, err := os.ReadFile(absfn)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("file content = %q, want %q", string(content), "hello world")
	}

	relfn := filepath.Base(absfn)

	if !tm.existsIDInTmpFiles(relfn) {
		t.Errorf("expected relfn %s to exist in tmpFiles", relfn)
	}

	id, ok := tm.getIDFromTmpFiles(relfn)
	if !ok || id != "test-id" {
		t.Errorf("getIDFromTmpFiles() = (%q, %t), want (%q, true)", id, ok, "test-id")
	}

	gotID, gotContent, err := tm.get(relfn)
	if err != nil {
		t.Fatalf("failed to get file: %v", err)
	}
	if gotID != "test-id" {
		t.Errorf("got id %q, want %q", gotID, "test-id")
	}
	if string(gotContent) != "hello world" {
		t.Errorf("got content %q, want %q", string(gotContent), "hello world")
	}

	tm.remove(msg, absfn)

	if _, err := os.Stat(absfn); !os.IsNotExist(err) {
		t.Errorf("file should have been deleted: %s", absfn)
	}

	if tm.existsIDInTmpFiles(relfn) {
		t.Errorf("relfn %s should have been deleted from tmpFiles", relfn)
	}

	outBytes := out.Bytes()
	if len(outBytes) < 4 {
		t.Fatalf("expected at least 4 bytes for length, got %d", len(outBytes))
	}
	msgLen := binary.LittleEndian.Uint32(outBytes[:4])
	rawJSON := outBytes[4 : 4+msgLen]

	var receivedMsg message
	if err := json.Unmarshal(rawJSON, &receivedMsg); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	if receivedMsg.Mtype != "death_notice" || receivedMsg.Payload.ID != "test-id" {
		t.Errorf("received message = %+v, want death_notice for test-id", receivedMsg)
	}
}

type trackingWriter struct {
	mu           sync.Mutex
	buf          bytes.Buffer
	textUpdated  chan struct{}
	deathNoticed chan struct{}
}

func (w *trackingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err = w.buf.Write(p)
	if err != nil {
		return n, err
	}

	if bytes.Contains(w.buf.Bytes(), []byte(`"text_update"`)) {
		select {
		case <-w.textUpdated:
		default:
			close(w.textUpdated)
		}
	}
	if bytes.Contains(w.buf.Bytes(), []byte(`"death_notice"`)) {
		select {
		case <-w.deathNoticed:
		default:
			close(w.deathNoticed)
		}
	}
	return n, nil
}

func (w *trackingWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := make([]byte, w.buf.Len())
	copy(b, w.buf.Bytes())
	return b
}

func TestRunnerIntegration(t *testing.T) {
	pr, pw := io.Pipe()
	trackOut := &trackingWriter{
		textUpdated:  make(chan struct{}),
		deathNoticed: make(chan struct{}),
	}

	r := newRunner(pr, trackOut, true)

	r.setExecCommand(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd
	})

	msg := message{}
	msg.Mtype = "new_text"
	msg.Payload.ID = "msg-1"
	msg.Payload.Text = "initial text"
	msg.Payload.Subject = "subject"
	msg.Payload.Editor = `["mock-editor"]`
	msg.Payload.Extension = "txt"

	rawMsg, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	msgLen := uint32(len(rawMsg))
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, msgLen)

	exitCodeChan := make(chan int, 1)
	go func() {
		exitCode := r.run()
		exitCodeChan <- exitCode
	}()

	pw.Write(lenBuf)
	pw.Write(rawMsg)

	select {
	case <-trackOut.textUpdated:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for text_update")
	}

	pw.Close()

	select {
	case <-trackOut.deathNoticed:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for death_notice")
	}

	select {
	case code := <-exitCodeChan:
		if code != 0 {
			t.Errorf("runner exited with non-zero code: %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for runner to finish")
	}

	outBytes := trackOut.Bytes()

	readMessages := []message{}
	offset := 0
	for offset < len(outBytes) {
		if offset+4 > len(outBytes) {
			t.Fatalf("incomplete length prefix at offset %d", offset)
		}
		l := binary.LittleEndian.Uint32(outBytes[offset : offset+4])
		offset += 4
		if offset+int(l) > len(outBytes) {
			t.Fatalf("incomplete message payload at offset %d, expected %d bytes", offset, l)
		}
		var m message
		if err := json.Unmarshal(outBytes[offset:offset+int(l)], &m); err != nil {
			t.Fatalf("failed to unmarshal output message: %v", err)
		}
		readMessages = append(readMessages, m)
		offset += int(l)
	}

	if len(readMessages) < 2 {
		t.Fatalf("expected at least 2 output messages, got %d: %+v", len(readMessages), readMessages)
	}

	for i := 0; i < len(readMessages)-1; i++ {
		m := readMessages[i]
		if m.Mtype != "text_update" {
			t.Errorf("message %d type = %q, want %q", i, m.Mtype, "text_update")
		}
		if m.Payload.Text != "updated content from mock editor\n" {
			t.Errorf("message %d text = %q, want %q", i, m.Payload.Text, "updated content from mock editor\n")
		}
		if m.Payload.ID != "msg-1" {
			t.Errorf("message %d id = %q, want %q", i, m.Payload.ID, "msg-1")
		}
	}

	lastMsg := readMessages[len(readMessages)-1]
	if lastMsg.Mtype != "death_notice" {
		t.Errorf("last message type = %q, want %q", lastMsg.Mtype, "death_notice")
	}
	if lastMsg.Payload.ID != "msg-1" {
		t.Errorf("last message id = %q, want %q", lastMsg.Payload.ID, "msg-1")
	}
}
