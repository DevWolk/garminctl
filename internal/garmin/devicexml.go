package garmin

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
}

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
	return d, nil
}

// SoftwareVersionString formats 1705 as "17.05".
func (d *DeviceXML) SoftwareVersionString() string {
	return fmt.Sprintf("%d.%02d", d.SoftwareVersion/100, d.SoftwareVersion%100)
}
