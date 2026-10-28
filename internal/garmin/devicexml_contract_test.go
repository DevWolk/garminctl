package garmin

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
)

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
