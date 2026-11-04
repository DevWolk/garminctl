// Package cli adapts Connect account and sync workflows to CLI input/output.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DevWolk/garminctl/internal/containers/connect"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/clix"

	"golang.org/x/term"
)

// Command borrows an account, scoped Connect workflow and job runner.
type Command struct {
	Out     *clix.Printer
	ErrOut  *clix.Printer
	JSON    bool
	Client  *gc.Client
	Cloud   *connect.Cloud
	RunSync func(context.Context, bool, bool, connect.Progress) (*connect.SyncResult, error)
}

func (c *Command) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.ErrOut)

	return fs
}

// Account handles sign-in/out without acquiring a watch.
func (c *Command) Account(ctx context.Context, args []string) error {
	client := c.Client

	switch args[0] {
	case "logout":
		if err := (connect.LogoutAction{Client: client}).Run(ctx); err != nil {
			return err
		}

		c.Out.Println("signed out")

		return nil
	case "mfa":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: cloud mfa CODE")
		}

		if err := (connect.MFAAction{Client: client}).Run(ctx, args[1]); err != nil {
			return err
		}

		c.Out.Println("signed in as", client.Email())

		return nil
	}

	return c.cloudLogin(ctx, client, args[1:])
}

func (c *Command) cloudLogin(ctx context.Context, client *gc.Client, args []string) error {
	fs := c.flagSet("cloud login")
	email := fs.String("email", os.Getenv("GARMIN_EMAIL"), "Garmin account email")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	if *email == "" {
		*email = c.prompt("Garmin account email: ")
	}

	password := os.Getenv("GARMIN_PASSWORD")
	if password == "" {
		var err error

		password, err = c.promptPassword()
		if err != nil {
			return err
		}
	}

	res, err := (connect.LoginAction{Client: client}).Run(ctx, *email, password)
	if err != nil {
		return err
	}

	if res.NeedsMFA {
		return c.cloudVerification(ctx, client, res.MFAMethod)
	}

	c.Out.Println("signed in as", client.Email())

	return nil
}

func (c *Command) cloudVerification(ctx context.Context, client *gc.Client, method string) error {
	code := ""

	if term.IsTerminal(int(os.Stdin.Fd())) {
		c.ErrOut.Printf("Garmin sent a verification code by %s. Enter it: ", method)

		var err error

		code, err = readVerificationCode(ctx, os.Stdin)
		if err != nil {
			return err
		}
	}

	if code == "" {
		c.Out.Printf("MFA code required (sent by %s); run: garminctl cloud mfa CODE\n", method)
		return nil
	}

	if err := (connect.MFAAction{Client: client}).Run(ctx, code); err != nil {
		return err
	}

	c.Out.Println("signed in as", client.Email())

	return nil
}

func readVerificationCode(ctx context.Context, input io.ReadCloser) (string, error) {
	type answer struct {
		line string
		err  error
	}

	answers := make(chan answer, 1)

	go func() {
		line, err := bufio.NewReader(input).ReadString('\n')
		answers <- answer{line: strings.TrimSpace(line), err: err}
	}()

	select {
	case result := <-answers:
		if result.err != nil && !errors.Is(result.err, io.EOF) {
			return "", result.err
		}

		return result.line, nil
	case <-ctx.Done():
		// Cancellation ends this CLI invocation, which owns its input handle.
		_ = input.Close()

		<-answers

		return "", ctx.Err()
	}
}

// prompt reads one trimmed line from stdin.
func (c *Command) prompt(label string) string {
	c.ErrOut.Print(label)

	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')

	return strings.TrimSpace(line)
}

// promptPassword reads a password without echo on a terminal.
func (c *Command) promptPassword() (string, error) {
	c.ErrOut.Print("Password: ")

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), nil
	}

	b, err := term.ReadPassword(int(os.Stdin.Fd()))

	c.ErrOut.Println()

	if err != nil {
		return "", err
	}

	return string(b), nil
}

// Run preserves existing bounded watch-command dispatch and formatting.
func (c *Command) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return clix.UsageErrorf("usage: cloud status|sync|messages|settings")
	}

	cl := c.Client
	cloud := c.Cloud

	switch args[0] {
	case "get", "post", "get1", "post1":
		if len(args) < 2 {
			return clix.UsageErrorf("usage: cloud get|post|get1|post1 URL [BODY]")
		}

		return c.cloudRaw(ctx, cl, args)
	case "status":
		return c.cloudStatus(ctx, cloud)
	case "sync":
		return c.cloudSync(ctx, args[1:])
	case "messages":
		return c.cloudMessages(ctx, cloud)
	case "settings":
		if len(args) >= 2 && args[1] == "set" {
			return c.cloudSettingsSet(ctx, cloud, args[2:])
		}

		return c.cloudSettings(ctx, cloud)
	}

	return clix.UsageErrorf("unknown cloud command %q", args[0])
}

// cloudRaw uses bearer authentication for get/post and OAuth1 for get1/post1.
func (c *Command) cloudRaw(ctx context.Context, cl *gc.Client, args []string) error {
	method, ct := http.MethodGet, ""

	var body []byte

	if strings.HasPrefix(args[0], "post") {
		method, ct = http.MethodPost, "application/json"

		if len(args) > 2 {
			body = []byte(args[2])
		}
	}

	var (
		st  int
		b   []byte
		err error
	)

	if strings.HasSuffix(args[0], "1") {
		st, b, err = cl.DoOAuth1(ctx, method, args[1], ct, body)
	} else {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}

		st, b, err = cl.Do(ctx, method, args[1], ct, r)
	}

	if err != nil {
		return err
	}

	c.Out.Printf("HTTP %d\n", st)

	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", " ") == nil {
		b = pretty.Bytes()
	}

	if _, err := c.Out.Write(b); err != nil {
		return err
	}

	c.Out.Println()

	return nil
}

func (c *Command) cloudStatus(ctx context.Context, cloud *connect.Cloud) error {
	st := (connect.StatusAction{Cloud: cloud}).Run(ctx)
	if c.JSON {
		return clix.PrintJSON(c.Out, st)
	}

	if !st.LoggedIn {
		if st.Error != "" {
			return fmt.Errorf("garmin connect: %s", st.Error)
		}

		c.Out.Println("not signed in (garminctl cloud login)")

		return nil
	}

	name := st.Email
	if st.Profile != nil && st.Profile.Name != "" {
		name = st.Profile.Name + " <" + st.Email + ">"
	}

	c.Out.Println("signed in as", name)

	if st.Registered {
		c.Out.Printf("watch registered on the account as %q\n", st.DeviceName)
	} else {
		c.Out.Println("watch is not registered on this account")
	}

	if st.Error != "" {
		c.Out.Println("warning:", st.Error)
	}

	return nil
}

func (c *Command) cloudSync(ctx context.Context, args []string) error {
	fs := c.flagSet("cloud sync")
	noUp := fs.Bool("no-upload", false, "skip uploading recorded files")
	noDown := fs.Bool("no-download", false, "skip pulling queued messages")

	if err := clix.ParseFlags(fs, args); err != nil {
		return err
	}

	progress := func(line string) {
		if !c.JSON {
			c.Out.Println(" ", line)
		}
	}

	res, err := c.RunSync(ctx, !*noUp, !*noDown, progress)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, res)
	}

	if res.Upload != nil {
		c.Out.Printf("uploaded %d, already in Connect %d, failed %d, unchanged %d\n", res.Upload.Uploaded, res.Upload.Duplicate, res.Upload.Failed, res.Upload.Skipped)
	}

	c.Out.Printf("pulled %d item(s) from Connect\n", len(res.Pulled))

	for _, e := range res.Errors {
		c.Out.Println("error:", e)
	}

	return nil
}

func (c *Command) cloudMessages(ctx context.Context, cloud *connect.Cloud) error {
	msgs, err := (connect.MessagesAction{Cloud: cloud}).Run(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, msgs)
	}

	if len(msgs) == 0 {
		c.Out.Println("nothing queued")
	}

	for _, m := range msgs {
		c.Out.Printf("%-10d %-18s %-8s %s\n", m.MessageID, m.MessageType, m.MessageStatus, m.MetaData.MessageName)
	}

	return nil
}

func (c *Command) cloudSettings(ctx context.Context, cloud *connect.Cloud) error {
	items, err := (connect.SettingsAction{Cloud: cloud}).Run(ctx)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, items)
	}

	for _, it := range items {
		v, _ := json.Marshal(it.Value)
		c.Out.Printf("%-28s %s\n", it.Key, v)
	}

	return nil
}

func (c *Command) cloudSettingsSet(ctx context.Context, cloud *connect.Cloud, kvs []string) error {
	changes := map[string]any{}

	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}

		changes[k] = clix.ParseScalar(v)
	}

	res, err := (connect.ApplySettingsAction{Cloud: cloud}).Run(ctx, changes)
	if err != nil {
		return err
	}

	if c.JSON {
		return clix.PrintJSON(c.Out, res)
	}

	c.Out.Println(res.Note)

	return nil
}
