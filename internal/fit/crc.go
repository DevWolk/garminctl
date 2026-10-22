package fit

var crcTable = [16]uint16{
	0x0000, 0xCC01, 0xD801, 0x1400, 0xF001, 0x3C00, 0x2800, 0xE401,
	0xA001, 0x6C00, 0x7800, 0xB401, 0x5000, 0x9C01, 0x8801, 0x4400,
}

// CRC computes the FIT CRC-16 of data.
func CRC(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc = crcByte(crc, b)
	}
	return crc
}

func crcByte(crc uint16, b byte) uint16 {
	tmp := crcTable[crc&0xF]
	crc = (crc >> 4) & 0x0FFF
	crc = crc ^ tmp ^ crcTable[b&0xF]
	tmp = crcTable[crc&0xF]
	crc = (crc >> 4) & 0x0FFF
	crc = crc ^ tmp ^ crcTable[(b>>4)&0xF]
	return crc
}
