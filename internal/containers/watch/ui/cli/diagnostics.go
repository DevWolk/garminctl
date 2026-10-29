package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/clix"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Hidden uses the vendor MTP listing to include hidden folders.
// -write-test writes, reads and deletes a temporary file in the same session.
func (c *Commands) Hidden(ctx context.Context, sess *watchsession.Session, args []string) error {
	fs := c.flagSet("hidden")
	writeTest := fs.Bool("write-test", false, "write and delete a temporary file in the folder")

	if err := clix.ParseFlags(fs, clix.FlagsFirst(fs, args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return clix.UsageErrorf("usage: hidden [-write-test] PATH")
	}

	err := sess.WithDiagnostics(ctx, func(dev *watchsession.Diagnostics) error {
		return c.hiddenDiagnostic(dev, fs.Arg(0), *writeTest)
	})
	if errors.Is(err, watchsession.ErrCapabilityUnavailable) {
		return errors.New("hidden requires the Windows MTP bridge")
	}

	return err
}

func (c *Commands) hiddenDiagnostic(dev *watchsession.Diagnostics, path string, writeTest bool) error {
	storage, handle, err := walkHidden(dev, path)
	if err != nil {
		return err
	}

	c.Out.Printf("%s: storage 0x%08X handle 0x%08X\n", path, storage, handle)

	kids, err := dev.AllChildren(storage, handle)
	if err != nil {
		return err
	}

	for _, k := range kids {
		c.Out.Printf("  0x%08X  %-24s %s %d\n", k.Handle, k.Name, onOff(k.IsDir(), "dir ", "file"), k.Size)
	}

	if !writeTest {
		return nil
	}

	return c.hiddenWriteTest(dev, storage, handle)
}

// walkHidden resolves a watch path to its storage and object handle, passing
// through hidden folders.
func walkHidden(dev *watchsession.Diagnostics, p string) (storage, handle uint32, err error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")

	storage, handle, err = dev.Handles("GARMIN")
	if err != nil {
		return 0, 0, err
	}

	if !strings.EqualFold(parts[0], "GARMIN") {
		storage, handle, err = dev.Handles(parts[0])
		if err != nil {
			return 0, 0, err
		}
	}

	for _, part := range parts[1:] {
		o, found, err := dev.FindChild(storage, handle, part)
		if err != nil {
			return 0, 0, err
		}

		if !found {
			return 0, 0, fmt.Errorf("%s: no %q under handle 0x%08X", p, part, handle)
		}

		handle = o.Handle
	}

	return storage, handle, nil
}

func (c *Commands) hiddenWriteTest(dev *watchsession.Diagnostics, storage, handle uint32) error {
	const name = "GCTLTEST.TMP"

	if old, found, err := dev.FindChild(storage, handle, name); err == nil && found {
		c.Out.Printf("leftover %s (handle 0x%08X, %d bytes) from an earlier run: deleting\n", name, old.Handle, old.Size)

		if err := dev.DeleteHandle(old.Handle); err != nil {
			return err
		}
	}

	data := []byte(strings.Repeat("garminctl write test\n", 50))

	h, err := dev.SendFile(storage, handle, name, data)
	if err != nil {
		return fmt.Errorf("write test: %w", err)
	}

	c.Out.Printf("wrote %s as handle 0x%08X (%d bytes)\n", name, h, len(data))

	o, found, err := dev.FindChild(storage, handle, name)
	c.Out.Printf("listed after write: found=%v size=%d err=%v\n", found, o.Size, err)

	back, err := dev.ReadHandle(h)
	c.Out.Printf("read back: %d bytes, identical=%v, err=%v\n", len(back), string(back) == string(data), err)

	if err := dev.DeleteHandle(h); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}

	_, found, err = dev.FindChild(storage, handle, name)
	c.Out.Printf("deleted; listed after delete: found=%v err=%v\n", found, err)

	return nil
}

// MTP reads a raw PTP operation; opcodes and parameters use hexadecimal.
func (c *Commands) MTP(ctx context.Context, sess *watchsession.Session, args []string) error {
	if !sess.SupportsRawMTP() || len(args) < 1 {
		return clix.UsageErrorf("usage: mtp OPCODE [PARAM...]  (requires the Windows MTP bridge)")
	}

	nums := make([]uint32, 0, len(args))

	for _, a := range args {
		n, err := parseHex32(a)
		if err != nil {
			return err
		}

		nums = append(nums, n)
	}

	data, code, rp, err := sess.RawMTPRead(ctx, nums[0], nums[1:])
	if err != nil {
		return err
	}

	c.Out.Printf("response 0x%04X params %v, %d bytes\n%s", code, rp, len(data), hex.Dump(data))

	return nil
}

// parseHex32 parses a hex number with or without a 0x prefix.
func parseHex32(s string) (uint32, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%q: %w", s, err)
	}

	return uint32(n), nil
}

func onOff(b bool, yes, no string) string {
	if b {
		return yes
	}

	return no
}
