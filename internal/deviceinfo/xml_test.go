package deviceinfo

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"strings"
	"testing"
)

// deviceXMLFixture is the shared vívoactive 5 metadata used by testdump.
const deviceXMLFixture = "../testdump/testdata/GarminDevice.xml"

func TestDeviceXMLWireContract(t *testing.T) {
	t.Parallel()

	const (
		source   = `<Device><Model><PartNumber>P-1</PartNumber><SoftwareVersion>1234</SoftwareVersion><Description>Example</Description></Model><Id>18446744073709551615</Id><MassStorageMode><DataType><Name>FIT_TYPE_4</Name><File><Location><Path>GARMIN/Activity</Path><BaseName>Activity</BaseName><FileExtension>fit</FileExtension></Location><TransferDirection>OutputFromUnit</TransferDirection></File></DataType></MassStorageMode><Extensions><IQAppExt><Apps><App><AppName>Audio</AppName><StoreId>store</StoreId><AppId>app</AppId><AppType>audio-content-provider-app</AppType><Version>7</Version><FileName>audio.PRG</FileName><MediaCacheSize>4294967296</MediaCacheSize></App></Apps></IQAppExt></Extensions></Device>`
		expected = `{"partNumber":"P-1","softwareVersion":1234,"description":"Example","unitId":18446744073709551615,"dataTypes":[{"name":"FIT_TYPE_4","files":[{"path":"GARMIN/Activity","baseName":"Activity","extension":"fit","direction":"OutputFromUnit"}]}],"apps":[{"name":"Audio","storeId":"store","appId":"app","type":"audio-content-provider-app","version":7,"fileName":"audio.PRG","mediaCacheBytes":4294967296}]}`
	)

	d, err := ParseDeviceXML([]byte(source))
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(d)
	if err != nil || string(data) != expected {
		t.Fatalf("metadata JSON=%s error=%v", data, err)
	}

	if d.SoftwareVersionString() != "12.34" || d.Apps[0].Type != AudioProviderType {
		t.Fatalf("metadata version/provider changed: %+v", d)
	}
}

func TestDeviceXMLMissingAndInvalidFieldsKeepTheirContracts(t *testing.T) {
	t.Parallel()

	d, err := ParseDeviceXML([]byte(`<Device/>`))
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(d)

	const empty = `{"partNumber":"","softwareVersion":0,"description":"","unitId":0,"dataTypes":null,"apps":null}`
	if err != nil || string(data) != empty {
		t.Fatalf("empty metadata JSON=%s error=%v", data, err)
	}

	_, err = ParseDeviceXML([]byte(`<Device>`))

	var syntax *xml.SyntaxError
	if !errors.As(err, &syntax) || !strings.HasPrefix(err.Error(), "GarminDevice.xml: ") {
		t.Fatalf("malformed metadata error=%v", err)
	}
}

func TestParseDeviceXML(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(deviceXMLFixture)
	if err != nil {
		t.Fatal(err)
	}

	d, err := ParseDeviceXML(b)
	if err != nil {
		t.Fatal(err)
	}

	if d.UnitID != 1234567890 || d.Description != "vívoactive 5" || d.SoftwareVersionString() != "17.05" {
		t.Fatalf("unexpected: %+v", *d)
	}

	if len(d.DataTypes) < 30 {
		t.Fatalf("expected many data types, got %d", len(d.DataTypes))
	}
}

func TestParseDeviceXMLApps(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(deviceXMLFixture)
	if err != nil {
		t.Fatal(err)
	}

	d, err := ParseDeviceXML(b)
	if err != nil {
		t.Fatal(err)
	}

	if len(d.Apps) == 0 {
		t.Fatal("no apps parsed from Extensions/IQAppExt/Apps")
	}

	var provider *XMLApp

	for i := range d.Apps {
		if d.Apps[i].Type == AudioProviderType {
			provider = &d.Apps[i]
		}
	}

	if provider == nil {
		t.Fatalf("no audio provider among %d apps", len(d.Apps))
	}

	if provider.Name == "" || provider.MediaCacheBytes <= 0 {
		t.Fatalf("provider %+v lacks name or cache size", *provider)
	}
}
