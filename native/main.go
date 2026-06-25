package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/fsnotify/fsnotify"
)

type myLogStruct struct{}

var (
	enable_log = false
	mylog      = &myLogStruct{}
	stdoutMu   sync.Mutex // Protects parallel writes to os.Stdout
)

func (l *myLogStruct) Printf(format string, v ...any) {
	if enable_log {
		log.Printf(format, v...)
	}
}

func (l *myLogStruct) Fatalf(format string, v ...any) {
	if enable_log {
		log.Fatalf(format, v...)
	}
}

type Message struct {
	Mtype   string `json:"type,omitempty"`
	Payload struct {
		Id        string `json:"id,omitempty"`
		Text      string `json:"text"`
		Caret     int    `json:"caret,omitempty"`
		Subject   string `json:"subject"`
		Editor    string `json:"editor"`
		Extension string `json:"extension"`
		Error     string `json:"error,omitempty"`
	} `json:"payload"`
}

type tmpManager struct {
	tmp_dir   string
	tmp_files map[string]string
	mu        *sync.RWMutex
}

func offset_to_line_and_column(msg *Message) (int, int) {
	rtext := []rune(msg.Payload.Text)
	offset := max(0, min(len(rtext), msg.Payload.Caret))
	subText := rtext[:offset]

	line := 0
	lastNewlineIdx := -1
	for i, r := range subText {
		if r == '\n' {
			line++
			lastNewlineIdx = i
		}
	}

	column := len(subText) - (lastNewlineIdx + 1)
	return line, column
}

func build_arguments(args []string, absfn string, line, column int) []string {
	expanded_args := make([]string, len(args), len(args)+1)
	fn_added := false
	replacer := strings.NewReplacer(
		"%s", absfn,
		"%l", strconv.Itoa(line+1),
		"%L", strconv.Itoa(line),
		"%c", strconv.Itoa(column+1),
		"%C", strconv.Itoa(column),
	)
	for i, arg := range args {
		if fn_added || strings.Contains(arg, "%s") {
			fn_added = true
		}
		output := replacer.Replace(arg)
		expanded_args[i] = output
	}
	if !fn_added {
		expanded_args = append(expanded_args, absfn)
	}
	return expanded_args
}

func send_raw_message(raw_msg []byte) {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()

	l := uint32(len(raw_msg))
	lbuf := make([]byte, 4)

	binary.LittleEndian.PutUint32(lbuf, l)

	os.Stdout.Write(lbuf)
	os.Stdout.Write(raw_msg)
	os.Stdout.Sync()

	mylog.Printf("send raw:%d, msg:[%s]\n", l, raw_msg)
}

func send_message(msg *Message) {
	raw_msg, err := json.Marshal(msg)
	if err != nil {
		mylog.Fatalf("internal error: json marshal\n")
		return
	}
	send_raw_message(raw_msg)
}

func send_text_update(id string, text string) {
	msg := &Message{}
	msg.Mtype = "text_update"
	msg.Payload.Id = id
	msg.Payload.Text = text
	send_message(msg)
}

func send_death_notice(id string) {
	msg := &Message{}
	msg.Mtype = "death_notice"
	msg.Payload.Id = id
	send_message(msg)
}

func send_error(err error) {
	msg := &Message{}
	msg.Mtype = "error"
	msg.Payload.Error = err.Error()
	send_message(msg)
}

func handle_inotify_event(ctx context.Context, tmp_mgr *tmpManager, targetAbsfn string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(targetAbsfn)
	if err != nil {
		return err
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher channel closed")
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				mylog.Printf("event:%s, file:%s\n", event, event.Name)
				id, bin, err := tmp_mgr.get(filepath.Base(targetAbsfn))
				if err != nil {
					return err
				}
				mylog.Printf("len msg:%d\n", len(bin))
				text := string(bin)
				replacer := strings.NewReplacer(
					"\r\n", "\n",
					"\r", "\n",
				)
				ln_text := replacer.Replace(text)
				send_text_update(id, ln_text)
			}
		case err, ok := <-watcher.Errors:
			mylog.Printf("watcher error:%s\n", err)
			if !ok {
				return errors.New("watcher error channel closed")
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func handle_message_new_text(ctx context.Context, tmp_mgr *tmpManager, msg *Message) error {
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	absfn, err := tmp_mgr.new(msg)
	if err != nil {
		send_error(err)
		return err
	}
	defer tmp_mgr.delete(msg, absfn)

	editor_args := []string{}
	if err := json.Unmarshal([]byte(msg.Payload.Editor), &editor_args); err != nil {
		mylog.Fatalf("Unmarshal error:editor_args:%s\n", err)
		return err
	}
	mylog.Printf("editor_args:%s\n", editor_args)

	if len(editor_args) == 0 {
		return errors.New("editor arguments are empty")
	}

	line, column := offset_to_line_and_column(msg)
	editor_args = build_arguments(editor_args, absfn, line, column)

	mylog.Printf("built editor_args: %s\n", editor_args)

	var watchWg sync.WaitGroup
	watchWg.Add(1)
	go func() {
		defer watchWg.Done()
		if err := handle_inotify_event(innerCtx, tmp_mgr, absfn); err != nil {
			mylog.Printf("handle_inotify_event error: %v\n", err)
		}
	}()

	cmd := exec.CommandContext(innerCtx, editor_args[0], editor_args[1:]...)
	err = cmd.Run()
	if err != nil {
		mylog.Printf("editor command execution error: %v\n", err)
	}

	mylog.Printf("exec command done: %v\n", err)

	cancel()
	watchWg.Wait()

	return err
}

func handle_message(ctx context.Context, tmp_mgr *tmpManager, msg *Message, wg *sync.WaitGroup) {
	switch msg.Mtype {
	case "new_text":
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := handle_message_new_text(ctx, tmp_mgr, msg); err != nil {
				mylog.Printf("handle_message_new_text error: %v\n", err)
			}
		}()
	}
}

func handle_stdin(ctx context.Context, tmp_mgr *tmpManager, wg *sync.WaitGroup) error {
	rawLenByte := make([]byte, 4)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := io.ReadFull(os.Stdin, rawLenByte)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				mylog.Printf("ReadFull len EOF\n")
				return nil
			}
			mylog.Fatalf("ReadFull len err: %v\n", err)
			return err
		}

		msgLen := binary.LittleEndian.Uint32(rawLenByte)
		mylog.Printf("stdin len:%d\n", msgLen)

		rawMsg := make([]byte, msgLen)
		_, err = io.ReadFull(os.Stdin, rawMsg)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				mylog.Printf("ReadFull msg EOF\n")
				return nil
			}
			mylog.Fatalf("ReadFull msg err: %v\n", err)
			return err
		}
		mylog.Printf("stdin len:%d, msg:%s\n", msgLen, rawMsg)

		msg := Message{}
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			mylog.Fatalf("Unmarshal err: %v\n", err)
			return err
		}
		handle_message(ctx, tmp_mgr, &msg, wg)
	}
}

func (tm *tmpManager) new(msg *Message) (string, error) {
	filename := func(s, m, e string) string {
		bstr := make([]byte, 0, len(s)+len(m)+len(e))
		r_underscore := rune('_')
		for _, r := range s {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				r = r_underscore
			}
			bstr = append(bstr, string(r)...)
		}
		bstr = append(bstr, m...)
		bstr = append(bstr, e...)
		return string(bstr)
	}(msg.Payload.Subject, "-*.", msg.Payload.Extension)

	f, err := os.CreateTemp(tm.tmp_dir, filename)
	if err != nil {
		mylog.Fatalf("CreateTemp err\n")
		return "", err
	}
	absfn := f.Name()

	mylog.Printf("msg, [%s]\n", []byte(msg.Payload.Text))
	if _, err := f.Write([]byte(msg.Payload.Text)); err != nil {
		mylog.Fatalf("Write Payload err\n")
		f.Close()
		return "", err
	}
	f.Close()

	relfn := filepath.Base(absfn)
	tm.setIdToTmpFiles(relfn, msg.Payload.Id)

	return absfn, nil
}

func (tm *tmpManager) get(relfn string) (string, []byte, error) {
	id, ok := tm.getIdFromTmpFiles(relfn)
	if !ok {
		return "", nil, errors.New("relfn does not exist in tmp_files")
	}

	text, err := os.ReadFile(filepath.Join(tm.tmp_dir, relfn))
	if err != nil {
		return "", nil, err
	}

	return id, text, nil
}

func (tm *tmpManager) setIdToTmpFiles(relfn string, id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tmp_files[relfn] = id
}

func (tm *tmpManager) getIdFromTmpFiles(relfn string) (string, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	v, ok := tm.tmp_files[relfn]
	return v, ok
}

func (tm *tmpManager) existsIdInTmpFiles(relfn string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	v, ok := tm.tmp_files[relfn]
	return (v != "") && ok
}

func (tm *tmpManager) deleteIdInTmpFiles(relfn string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.tmp_files, relfn)
}

func (tm *tmpManager) delete(msg *Message, absfn string) {
	relfn := filepath.Base(absfn)
	tm.deleteIdInTmpFiles(relfn)
	os.Remove(absfn)
	send_death_notice(msg.Payload.Id)
}

func NewTmpManager() (*tmpManager, error) {
	td := ""
	if runtime.GOOS == "windows" {
		t := os.TempDir()
		td = filepath.Join(t, "exteditor")
	} else {
		t := os.Getenv("XDG_RUNTIME_DIR")
		if t != "" {
			td = filepath.Join(t, "exteditor")
		} else {
			t := os.TempDir()
			if filepath.Base(t) == "tmp" {
				td = filepath.Join(os.TempDir(), os.Getenv("USER"), "exteditor")
			} else {
				td = filepath.Join(os.TempDir(), "exteditor")
			}
		}
	}

	err := os.MkdirAll(td, 0700)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(td, "*")
	if err != nil {
		return nil, err
	}

	ret := &tmpManager{tmp_dir: dir, tmp_files: map[string]string{}}
	ret.mu = &sync.RWMutex{}
	return ret, nil
}

func run() int {
	mylog.Printf("start run\n")

	tmp_mgr, err := NewTmpManager()
	if err != nil {
		mylog.Fatalf("something error occurs when create temporary directory:%s", err.Error())
		return 1
	}
	defer os.RemoveAll(tmp_mgr.tmp_dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := handle_stdin(ctx, tmp_mgr, &wg); err != nil {
			mylog.Printf("handle_stdin error: %v\n", err)
		}
		cancel()
	}()

	wg.Wait()

	return 0
}

func main() {
	if enable_log {
		log.SetPrefix("[Log] ")
		f, err := os.Create("log.txt")
		if err != nil {
			log.Fatalf("log err:%s", err.Error())
			os.Exit(1)
		}
		log.SetOutput(f)
	}

	exit_code := run()

	os.Exit(exit_code)
}
