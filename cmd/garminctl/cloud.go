package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"

	"golang.org/x/term"
)

// cloudAccount handles the sign-in commands, which need no watch.
func (c *cli) cloudAccount(ctx context.Context, args []string) error {
	client := gc.New(gc.DefaultStorePath())

	switch args[0] {
	case "logout":
		if err := client.Logout(); err != nil {
			return err
		}

		c.out.Println("signed out")

		return nil
	case "mfa":
		if len(args) < 2 {
			return usageErrorf("usage: cloud mfa CODE")
		}

		if err := client.ResumeMFA(ctx, args[1]); err != nil {
			return err
		}

		c.out.Println("signed in as", client.Email())

		return nil
	}

	return c.cloudLogin(ctx, client, args[1:])
}

func (c *cli) cloudLogin(ctx context.Context, client *gc.Client, args []string) error {
	fs := c.flagSet("cloud login")
	email := fs.String("email", os.Getenv("GARMIN_EMAIL"), "Garmin account email")

	if err := parseFlags(fs, args); err != nil {
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

	res, err := client.Login(ctx, *email, password)
	if err != nil {
		return err
	}

	if res.NeedsMFA {
		return c.cloudVerification(ctx, client, res.MFAMethod)
	}

	c.out.Println("signed in as", client.Email())

	return nil
}

func (c *cli) cloudVerification(ctx context.Context, client *gc.Client, method string) error {
	code := ""

	if term.IsTerminal(int(os.Stdin.Fd())) {
		c.errOut.Printf("Garmin sent a verification code by %s. Enter it: ", method)

		var err error

		code, err = readVerificationCode(ctx, os.Stdin)
		if err != nil {
			return err
		}
	}

	if code == "" {
		c.out.Printf("MFA code required (sent by %s); run: garminctl cloud mfa CODE\n", method)
		return nil
	}

	if err := client.ResumeMFA(ctx, code); err != nil {
		return err
	}

	c.out.Println("signed in as", client.Email())

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
func (c *cli) prompt(label string) string {
	c.errOut.Print(label)

	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')

	return strings.TrimSpace(line)
}

// promptPassword reads a password without echo on a terminal.
func (c *cli) promptPassword() (string, error) {
	c.errOut.Print("Password: ")

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), nil
	}

	b, err := term.ReadPassword(int(os.Stdin.Fd()))

	c.errOut.Println()

	if err != nil {
		return "", err
	}

	return string(b), nil
}

func (c *cli) cloud(ctx context.Context, svc *garmin.Service, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: cloud status|sync|messages|settings")
	}

	cl := gc.New(gc.DefaultStorePath())
	cloud := garmin.NewCloud(svc, cl)

	switch args[0] {
	case "get", "post", "get1", "post1":
		if len(args) < 2 {
			return usageErrorf("usage: cloud get|post|get1|post1 URL [BODY]")
		}

		return c.cloudRaw(ctx, cl, args)
	case "status":
		return c.cloudStatus(ctx, cloud)
	case "sync":
		return c.cloudSync(ctx, cloud, args[1:])
	case "messages":
		return c.cloudMessages(ctx, cloud)
	case "settings":
		if len(args) >= 2 && args[1] == "set" {
			return c.cloudSettingsSet(ctx, cloud, args[2:])
		}

		return c.cloudSettings(ctx, cloud)
	}

	return usageErrorf("unknown cloud command %q", args[0])
}

// cloudRaw uses bearer authentication for get/post and OAuth1 for get1/post1.
func (c *cli) cloudRaw(ctx context.Context, cl *gc.Client, args []string) error {
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

	c.out.Printf("HTTP %d\n", st)

	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", " ") == nil {
		b = pretty.Bytes()
	}

	if _, err := c.out.Write(b); err != nil {
		return err
	}

	c.out.Println()

	return nil
}

func (c *cli) cloudStatus(ctx context.Context, cloud *garmin.Cloud) error {
	st := cloud.Status(ctx)
	if c.asJSON {
		return c.printJSON(st)
	}

	if !st.LoggedIn {
		if st.Error != "" {
			return fmt.Errorf("garmin connect: %s", st.Error)
		}

		c.out.Println("not signed in (garminctl cloud login)")

		return nil
	}

	name := st.Email
	if st.Profile != nil && st.Profile.Name != "" {
		name = st.Profile.Name + " <" + st.Email + ">"
	}

	c.out.Println("signed in as", name)

	if st.Registered {
		c.out.Printf("watch registered on the account as %q\n", st.DeviceName)
	} else {
		c.out.Println("watch is not registered on this account")
	}

	if st.Error != "" {
		c.out.Println("warning:", st.Error)
	}

	return nil
}

func (c *cli) cloudSync(ctx context.Context, cloud *garmin.Cloud, args []string) error {
	fs := c.flagSet("cloud sync")
	noUp := fs.Bool("no-upload", false, "skip uploading recorded files")
	noDown := fs.Bool("no-download", false, "skip pulling queued messages")

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	res, err := cloud.Sync(ctx, !*noUp, !*noDown, func(line string) {
		if !c.asJSON {
			c.out.Println(" ", line)
		}
	})
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(res)
	}

	if res.Upload != nil {
		c.out.Printf("uploaded %d, already in Connect %d, failed %d, unchanged %d\n", res.Upload.Uploaded, res.Upload.Duplicate, res.Upload.Failed, res.Upload.Skipped)
	}

	c.out.Printf("pulled %d item(s) from Connect\n", len(res.Pulled))

	for _, e := range res.Errors {
		c.out.Println("error:", e)
	}

	return nil
}

func (c *cli) cloudMessages(ctx context.Context, cloud *garmin.Cloud) error {
	msgs, err := cloud.Messages(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(msgs)
	}

	if len(msgs) == 0 {
		c.out.Println("nothing queued")
	}

	for _, m := range msgs {
		c.out.Printf("%-10d %-18s %-8s %s\n", m.MessageID, m.MessageType, m.MessageStatus, m.MetaData.MessageName)
	}

	return nil
}

func (c *cli) cloudSettings(ctx context.Context, cloud *garmin.Cloud) error {
	items, err := cloud.Settings(ctx)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(items)
	}

	for _, it := range items {
		v, _ := json.Marshal(it.Value)
		c.out.Printf("%-28s %s\n", it.Key, v)
	}

	return nil
}

func (c *cli) cloudSettingsSet(ctx context.Context, cloud *garmin.Cloud, kvs []string) error {
	changes := map[string]any{}

	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", kv)
		}

		changes[k] = parseScalar(v)
	}

	res, err := cloud.SetSettings(ctx, changes)
	if err != nil {
		return err
	}

	if c.asJSON {
		return c.printJSON(res)
	}

	c.out.Println(res.Note)

	return nil
}
