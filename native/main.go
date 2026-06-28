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

var enableLog = false

type myLogStruct struct {
	r *runner
}

func (l *myLogStruct) Printf(format string, v ...any) {
	if l.r.enableLog {
		log.Printf(format, v...)
	}
}

func (l *myLogStruct) Fatalf(format string, v ...any) {
	if l.r.enableLog {
		log.Fatalf(format, v...)
	}
}

type message struct {
	Mtype   string `json:"type,omitempty"`
	Payload struct {
		ID        string `json:"id,omitempty"`
		Text      string `json:"text"`
		Caret     int    `json:"caret,omitempty"`
		Subject   string `json:"subject"`
		Editor    string `json:"editor"`
		Extension string `json:"extension"`
		Error     string `json:"error,omitempty"`
	} `json:"payload"`
}

type tmpManager struct {
	tmpDir   string
	tmpFiles map[string]string
	mu       *sync.RWMutex
	r        *runner
}

type runner struct {
	in          io.Reader
	out         io.Writer
	stdoutMu    sync.Mutex
	enableLog   bool
	mylog       *myLogStruct
	execCommand func(ctx context.Context, name string, arg ...string) *exec.Cmd
	tmpMgr      *tmpManager
}

func newRunner(in io.Reader, out io.Writer, enableLog bool) *runner {
	r := &runner{
		in:          in,
		out:         out,
		enableLog:   enableLog,
		execCommand: exec.CommandContext,
	}
	r.mylog = &myLogStruct{r: r}
	return r
}

func offsetToLineAndColumn(msg *message) (int, int) {
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

func buildArguments(args []string, absfn string, line, column int) []string {
	expandedArgs := make([]string, len(args), len(args)+1)
	fnAdded := false
	replacer := strings.NewReplacer(
		"%s", absfn,
		"%l", strconv.Itoa(line+1),
		"%L", strconv.Itoa(line),
		"%c", strconv.Itoa(column+1),
		"%C", strconv.Itoa(column),
	)
	for i, arg := range args {
		if fnAdded || strings.Contains(arg, "%s") {
			fnAdded = true
		}
		output := replacer.Replace(arg)
		expandedArgs[i] = output
	}
	if !fnAdded {
		expandedArgs = append(expandedArgs, absfn)
	}
	return expandedArgs
}

func (r *runner) sendRawMessage(rawMsg []byte) {
	r.stdoutMu.Lock()
	defer r.stdoutMu.Unlock()

	l := uint32(len(rawMsg))
	lbuf := make([]byte, 4)

	binary.LittleEndian.PutUint32(lbuf, l)

	r.out.Write(lbuf)
	r.out.Write(rawMsg)
	if syncer, ok := r.out.(interface{ Sync() error }); ok {
		syncer.Sync()
	}

	r.mylog.Printf("send raw:%d, msg:[%s]\n", l, rawMsg)
}

func (r *runner) sendMessage(msg *message) {
	rawMsg, err := json.Marshal(msg)
	if err != nil {
		r.mylog.Fatalf("internal error: json marshal\n")
		return
	}
	r.sendRawMessage(rawMsg)
}

func (r *runner) sendTextUpdate(id string, text string) {
	msg := &message{}
	msg.Mtype = "text_update"
	msg.Payload.ID = id
	msg.Payload.Text = text
	r.sendMessage(msg)
}

func (r *runner) sendDeathNotice(id string) {
	msg := &message{}
	msg.Mtype = "death_notice"
	msg.Payload.ID = id
	r.sendMessage(msg)
}

func (r *runner) sendError(err error) {
	msg := &message{}
	msg.Mtype = "error"
	msg.Payload.Error = err.Error()
	r.sendMessage(msg)
}

func (r *runner) handleInotifyEvent(ctx context.Context, tm *tmpManager, targetAbsfn string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add(filepath.Dir(targetAbsfn))
	if err != nil {
		return err
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("watcher channel closed")
			}
			if event.Name != targetAbsfn {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				r.mylog.Printf("event:%s, file:%s\n", event, event.Name)
				id, bin, err := tm.get(filepath.Base(targetAbsfn))
				if err != nil {
					return err
				}
				r.mylog.Printf("len msg:%d\n", len(bin))
				text := string(bin)
				replacer := strings.NewReplacer(
					"\r\n", "\n",
					"\r", "\n",
				)
				lnText := replacer.Replace(text)
				r.sendTextUpdate(id, lnText)
			}
		case err, ok := <-watcher.Errors:
			r.mylog.Printf("watcher error:%s\n", err)
			if !ok {
				return errors.New("watcher error channel closed")
			}
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func (r *runner) handleMessageNewText(ctx context.Context, tm *tmpManager, msg *message) error {
	innerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	absfn, err := tm.create(msg)
	if err != nil {
		r.sendError(err)
		return err
	}
	defer tm.remove(msg, absfn)

	editorArgs := []string{}
	if err := json.Unmarshal([]byte(msg.Payload.Editor), &editorArgs); err != nil {
		r.mylog.Fatalf("Unmarshal error:editor_args:%s\n", err)
		return err
	}
	r.mylog.Printf("editor_args:%s\n", editorArgs)

	if len(editorArgs) == 0 {
		return errors.New("editor arguments are empty")
	}

	line, column := offsetToLineAndColumn(msg)
	editorArgs = buildArguments(editorArgs, absfn, line, column)

	r.mylog.Printf("built editor_args: %s\n", editorArgs)

	var watchWg sync.WaitGroup
	watchWg.Go(func() {
		if err := r.handleInotifyEvent(innerCtx, tm, absfn); err != nil {
			r.mylog.Printf("handle_inotify_event error: %v\n", err)
		}
	})

	cmd := r.execCommand(innerCtx, editorArgs[0], editorArgs[1:]...)
	err = cmd.Run()
	if err != nil {
		r.mylog.Printf("editor command execution error: %v\n", err)
	}

	r.mylog.Printf("exec command done: %v\n", err)

	cancel()
	watchWg.Wait()

	return err
}

func (r *runner) handleMessage(ctx context.Context, tm *tmpManager, msg *message, wg *sync.WaitGroup) {
	switch msg.Mtype {
	case "new_text":
		wg.Go(func() {
			if err := r.handleMessageNewText(ctx, tm, msg); err != nil {
				r.mylog.Printf("handle_message_new_text error: %v\n", err)
			}
		})
	}
}

func (r *runner) handleStdin(ctx context.Context, tm *tmpManager, wg *sync.WaitGroup) error {
	rawLenByte := make([]byte, 4)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, err := io.ReadFull(r.in, rawLenByte)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				r.mylog.Printf("ReadFull len EOF\n")
				return nil
			}
			r.mylog.Fatalf("ReadFull len err: %v\n", err)
			return err
		}

		msgLen := binary.LittleEndian.Uint32(rawLenByte)
		r.mylog.Printf("stdin len:%d\n", msgLen)

		rawMsg := make([]byte, msgLen)
		_, err = io.ReadFull(r.in, rawMsg)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				r.mylog.Printf("ReadFull msg EOF\n")
				return nil
			}
			r.mylog.Fatalf("ReadFull msg err: %v\n", err)
			return err
		}
		r.mylog.Printf("stdin len:%d, msg:%s\n", msgLen, rawMsg)

		msg := message{}
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			r.mylog.Fatalf("Unmarshal err: %v\n", err)
			return err
		}
		r.handleMessage(ctx, tm, &msg, wg)
	}
}

func (tm *tmpManager) create(msg *message) (string, error) {
	filename := func(s, m, e string) string {
		bstr := make([]byte, 0, len(s)+len(m)+len(e))
		rUnderscore := rune('_')
		for _, r := range s {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				r = rUnderscore
			}
			bstr = append(bstr, string(r)...)
		}
		bstr = append(bstr, m...)
		bstr = append(bstr, e...)
		return string(bstr)
	}(msg.Payload.Subject, "-*.", msg.Payload.Extension)

	f, err := os.CreateTemp(tm.tmpDir, filename)
	if err != nil {
		tm.r.mylog.Fatalf("CreateTemp err\n")
		return "", err
	}
	absfn := f.Name()

	tm.r.mylog.Printf("msg, [%s]\n", []byte(msg.Payload.Text))
	if _, err := f.Write([]byte(msg.Payload.Text)); err != nil {
		tm.r.mylog.Fatalf("Write Payload err\n")
		f.Close()
		return "", err
	}
	f.Close()

	relfn := filepath.Base(absfn)
	tm.setIDToTmpFiles(relfn, msg.Payload.ID)

	return absfn, nil
}

func (tm *tmpManager) get(relfn string) (string, []byte, error) {
	id, ok := tm.getIDFromTmpFiles(relfn)
	if !ok {
		return "", nil, errors.New("relfn does not exist in tmpFiles")
	}

	text, err := os.ReadFile(filepath.Join(tm.tmpDir, relfn))
	if err != nil {
		return "", nil, err
	}

	return id, text, nil
}

func (tm *tmpManager) setIDToTmpFiles(relfn string, id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tmpFiles[relfn] = id
}

func (tm *tmpManager) getIDFromTmpFiles(relfn string) (string, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	v, ok := tm.tmpFiles[relfn]
	return v, ok
}

func (tm *tmpManager) existsIDInTmpFiles(relfn string) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	v, ok := tm.tmpFiles[relfn]
	return (v != "") && ok
}

func (tm *tmpManager) deleteIDInTmpFiles(relfn string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.tmpFiles, relfn)
}

func (tm *tmpManager) remove(msg *message, absfn string) {
	relfn := filepath.Base(absfn)
	tm.deleteIDInTmpFiles(relfn)
	os.Remove(absfn)
	tm.r.sendDeathNotice(msg.Payload.ID)
}

func (r *runner) newTmpManager() (*tmpManager, error) {
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

	ret := &tmpManager{tmpDir: dir, tmpFiles: map[string]string{}, r: r}
	ret.mu = &sync.RWMutex{}
	return ret, nil
}

func (r *runner) run() int {
	r.mylog.Printf("start run\n")

	tm, err := r.newTmpManager()
	if err != nil {
		r.mylog.Fatalf("something error occurs when create temporary directory:%s", err.Error())
		return 1
	}
	defer os.RemoveAll(tm.tmpDir)
	r.tmpMgr = tm

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	wg.Go(func() {
		if err := r.handleStdin(ctx, tm, &wg); err != nil {
			r.mylog.Printf("handle_stdin error: %v\n", err)
		}
		cancel()
	})

	wg.Wait()

	return 0
}

func (r *runner) setExecCommand(f func(ctx context.Context, name string, arg ...string) *exec.Cmd) {
	r.execCommand = f
}

func main() {
	if enableLog {
		log.SetPrefix("[Log] ")
		f, err := os.Create("log.txt")
		if err != nil {
			log.Fatalf("log err:%s", err.Error())
			os.Exit(1)
		}
		log.SetOutput(f)
	}

	r := newRunner(os.Stdin, os.Stdout, enableLog)
	exitCode := r.run()

	os.Exit(exitCode)
}
