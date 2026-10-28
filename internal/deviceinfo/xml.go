// Package deviceinfo decodes watch metadata and names its XML wire types.
// It owns no device connection, cache or feature policy.
package deviceinfo

import (
	"encoding/xml"
	"fmt"
)

// DeviceXML is the subset of GARMIN/GarminDevice.xml the tool uses.
type DeviceXML struct {
	PartNumber      string     `json:"partNumber"`
	SoftwareVersion int        `json:"softwareVersion"`
	Description     string     `json:"description"`
	UnitID          uint64     `json:"unitId"`
	DataTypes       []DataType `json:"dataTypes"`
	Apps            []XMLApp   `json:"apps"`
}

// XMLApp is a Connect IQ app as the device XML's Apps extension lists it.
// Audio content providers may include their media cache size.
type XMLApp struct {
	Name            string `json:"name"`
	StoreID         string `json:"storeId"`
	AppID           string `json:"appId"`
	Type            string `json:"type"` // watch-app, watchface, widget, data-field, audio-content-provider-app …
	Version         int    `json:"version"`
	FileName        string `json:"fileName"`
	MediaCacheBytes int64  `json:"mediaCacheBytes,omitempty"`
}

// AudioProviderType is the device-XML AppType of streaming music apps.
const AudioProviderType = "audio-content-provider-app"

// DataType is one file type the device exchanges.
type DataType struct {
	Name  string         `json:"name"`
	Files []DataTypeFile `json:"files"`
}

// DataTypeFile is one location of a data type.
type DataTypeFile struct {
	Path      string `json:"path"`
	BaseName  string `json:"baseName,omitempty"`
	Extension string `json:"extension"`
	Direction string `json:"direction"`
}

type xmlDevice struct {
	Model struct {
		PartNumber      string `xml:"PartNumber"`
		SoftwareVersion int    `xml:"SoftwareVersion"`
		Description     string `xml:"Description"`
	} `xml:"Model"`
	ID              uint64 `xml:"Id"`
	MassStorageMode struct {
		DataType []struct {
			Name string `xml:"Name"`
			File []struct {
				Location struct {
					Path          string `xml:"Path"`
					BaseName      string `xml:"BaseName"`
					FileExtension string `xml:"FileExtension"`
				} `xml:"Location"`
				TransferDirection string `xml:"TransferDirection"`
			} `xml:"File"`
		} `xml:"DataType"`
	} `xml:"MassStorageMode"`
	Apps []struct {
		AppName        string `xml:"AppName"`
		StoreID        string `xml:"StoreId"`
		AppID          string `xml:"AppId"`
		AppType        string `xml:"AppType"`
		Version        int    `xml:"Version"`
		FileName       string `xml:"FileName"`
		MediaCacheSize int64  `xml:"MediaCacheSize"`
	} `xml:"Extensions>IQAppExt>Apps>App"`
}

// ParseDeviceXML decodes GarminDevice.xml.
func ParseDeviceXML(b []byte) (*DeviceXML, error) {
	var x xmlDevice
	if err := xml.Unmarshal(b, &x); err != nil {
		return nil, fmt.Errorf("GarminDevice.xml: %w", err)
	}

	d := &DeviceXML{PartNumber: x.Model.PartNumber, SoftwareVersion: x.Model.SoftwareVersion, Description: x.Model.Description, UnitID: x.ID}
	for _, dt := range x.MassStorageMode.DataType {
		t := DataType{Name: dt.Name}
		for _, f := range dt.File {
			t.Files = append(t.Files, DataTypeFile{Path: f.Location.Path, BaseName: f.Location.BaseName, Extension: f.Location.FileExtension, Direction: f.TransferDirection})
		}

		d.DataTypes = append(d.DataTypes, t)
	}

	for _, a := range x.Apps {
		d.Apps = append(d.Apps, XMLApp{Name: a.AppName, StoreID: a.StoreID, AppID: a.AppID, Type: a.AppType, Version: a.Version, FileName: a.FileName, MediaCacheBytes: a.MediaCacheSize})
	}

	return d, nil
}

// SoftwareVersionString formats the version in hundredths (1234 becomes "12.34").
func (d *DeviceXML) SoftwareVersionString() string {
	return fmt.Sprintf("%d.%02d", d.SoftwareVersion/100, d.SoftwareVersion%100)
}
