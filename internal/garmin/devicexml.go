package garmin

import "github.com/DevWolk/garminctl/internal/deviceinfo"

// DeviceXML preserves the legacy name while new features import deviceinfo.
type DeviceXML = deviceinfo.DeviceXML

// XMLApp preserves the device XML app entry during feature extraction.
type XMLApp = deviceinfo.XMLApp

// DataType preserves the legacy file-type metadata name.
type DataType = deviceinfo.DataType

// DataTypeFile preserves the legacy transfer-location metadata name.
type DataTypeFile = deviceinfo.DataTypeFile

// AudioProviderType retains the device XML's audio-provider type name.
const AudioProviderType = deviceinfo.AudioProviderType

// ParseDeviceXML delegates metadata parsing to the pure deviceinfo leaf.
func ParseDeviceXML(b []byte) (*DeviceXML, error) { return deviceinfo.ParseDeviceXML(b) }
