package settings

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestSettingsBuildChecksRawBeforeNarrowing(t *testing.T) {
	t.Parallel()

	for _, raw := range []int64{math.MinInt32 - 1, math.MaxUint32 + 1, math.MaxInt64} {
		data, err := BuildChangeFile([]ResolvedChange{{MesgNum: 2, FieldNum: 58, Raw: raw}}, 1, 1, time.Unix(1700000000, 0))
		if err == nil || data != nil {
			t.Fatalf("raw %d produced partial file: %d bytes, %v", raw, len(data), err)
		}
	}

	for _, spec := range []string{"2[-1].58=1", "2[65536].58=1", "2.58[-1]=1", "2.58[256]=1"} {
		if _, err := ParseChangeSpec(spec); err == nil {
			t.Fatalf("invalid index accepted: %s", spec)
		}
	}
}

func TestGeneratedSettingsFITPreservesExistingBytes(t *testing.T) {
	t.Parallel()

	index := 4
	changes := []ResolvedChange{
		{MesgNum: 2, FieldNum: 58, Raw: 1500},
		{Index: 5, ArrayIndex: &index, MesgNum: 2, FieldNum: 9, Raw: -123},
	}

	// Captured from the parent implementation before changing error handling.
	data, err := BuildChangeFile(changes, 1234567890, 1705, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}

	testdump.CheckDigest(t, data, "d81bf08eea4d9d3d63c4b241085a939ecd3e0f829d454e99113ee861f79ca8f7")

	if _, err := fit.Decode(data); err != nil {
		t.Fatal(err)
	}
}

func TestBuildChangeFilePropagatesEncodingFailure(t *testing.T) {
	t.Parallel()

	arrayIndex := math.MaxUint8 + 1
	for _, change := range []ResolvedChange{
		{Index: math.MaxUint16 + 1, MesgNum: 2, FieldNum: 58, Raw: 1500},
		{ArrayIndex: &arrayIndex, MesgNum: 2, FieldNum: 58, Raw: 1500},
	} {
		data, err := BuildChangeFile([]ResolvedChange{change}, 1, 1, time.Unix(1700000000, 0))
		if err == nil || data != nil {
			t.Fatalf("invalid field produced a partial FIT: %x, %v", data, err)
		}
	}
}

func TestGeneratedSettingsFITUsesLow32SerialBits(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0)

	const unit = uint64(0xfedcba9876543210)

	data, err := BuildChangeFile(nil, unit, 1705, now)
	if err != nil {
		t.Fatal(err)
	}

	low, err := BuildChangeFile(nil, 0x76543210, 1705, now)
	if err != nil || !bytes.Equal(data, low) {
		t.Fatalf("settings serial boundary changed: %v", err)
	}

	decoded, err := fit.Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	if got := decoded.MessagesByNum(0)[0].Value(3); got != uint64(0x76543210) {
		t.Fatalf("decoded serial = %#v", got)
	}
}
