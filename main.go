package main

import (
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"io"
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/davecgh/go-spew/spew"
)

func main() {

	logrus.SetLevel(logrus.DebugLevel)

	// f, _ := os.Open("data/KCRP20170826_235827_V06")
	f, _ := os.Open("data/KGRK20170827_234611_V06")
	fi, _ := f.Stat()

	// read in the volume header record
	vhr := VolumeHeaderRecord{}
	binary.Read(f, binary.BigEndian, &vhr)

	// The first LDMRecord is the Metadata Record, consisting of 134 messages of
	// Metadata message types 15, 13, 18, 3, 5, and 2
	logrus.Debugf("-------------- LDM Compressed Record (Metadata) ---------------")
	ldm := LDMRecord{}
	binary.Read(f, binary.BigEndian, &ldm.Size)
	decompress(f, ldm.Size)

	// Following the first LDM Metadata Record is a variable number of compressed
	// records containing 120 radial messages (type 31) plus 0 or more RDA Status
	// messages (type 2).
	for true {
		ldm := LDMRecord{}

		// read in control word (size) of LDM record
		if err := binary.Read(f, binary.BigEndian, &ldm.Size); err != nil {
			if err == io.EOF {
				logrus.Debug("reached EOF")
				return
			}
			logrus.Panic(err.Error())
		}

		// As the control word contains a negative size under some circumstances,
		// the absolute value of the control word must be used for determining
		// the size of the block.
		if ldm.Size < 0 {
			ldm.Size = -ldm.Size
		}

		bytesRead, _ := f.Seek(0, io.SeekCurrent)
		pctComplete := float64(bytesRead) / float64(fi.Size()) * 100
		logrus.Debugf("---------------- LDM Compressed Record (%4.1f%%) ----------------", pctComplete)

		msgBuf := decompress(f, ldm.Size)

		for true {

			// 12 byte offset is due to legacy compliance of CTM Header
			msgBuf.Seek(12, io.SeekCurrent)

			header := MessageHeader{}
			if err := binary.Read(msgBuf, binary.BigEndian, &header); err != nil {
				if err != io.EOF {
					logrus.Panic(err.Error())
				}
				break
			}

			// logrus.Debugf("== Message %d", header.MessageType)

			switch header.MessageType {
			case 0:
				spew.Dump(header)
				msg := make([]byte, header.MessageSize)
				binary.Read(msgBuf, binary.BigEndian, &msg)
				spew.Dump(msg)
				return
			case 31:
				msg31(msgBuf)
				// logrus.Infof("\tAzimuth Number: %d", m31.Header.AzimuthNumber)
				// logrus.Infof("\tAzimuth Angle: %f", m31.Header.AzimuthAngle)
				// logrus.Infof("\tAzimuth Res Spacing: %d", m31.Header.AzimuthResolutionSpacing)
				// logrus.Infof("\tElevation Angle: %f", m31.Header.ElevationAngle)
				// logrus.Infof("\tElevation Number: %d", m31.Header.ElevationNumber)
				// logrus.Infof("\tRadialStatus: %d", m31.Header.RadialStatus)
				// logrus.Infof("\tRadial Length: %d", m31.Header.RadialLength)
				// logrus.Infof("\tCut Sector Num: %d", m31.Header.CutSectorNumber)
			case 2:
				m2 := RDAStatusMessage2{}
				binary.Read(msgBuf, binary.BigEndian, &m2)
				// eat the rest of the record since we know it's 2432 bytes
				msg := make([]byte, 2432-16-54-12)
				binary.Read(msgBuf, binary.BigEndian, &msg)
			default:
				spew.Dump(header)
				// eat the rest of the record since we know it's 2432 bytes (2416 - header)
				msg := make([]byte, 2416)
				binary.Read(msgBuf, binary.BigEndian, &msg)
				spew.Dump(msg)
			}
		}
	}
}

func preview(r io.ReadSeeker, n int) {
	preview := make([]byte, n)
	binary.Read(r, binary.BigEndian, &preview)
	spew.Dump(preview)
	r.Seek(-int64(n), io.SeekCurrent)
}

func decompress(f *os.File, size int32) *bytes.Reader {
	compressedData := make([]byte, size)
	binary.Read(f, binary.BigEndian, &compressedData)
	bz2Reader := bzip2.NewReader(bytes.NewReader(compressedData))
	extractedData := bytes.NewBuffer([]byte{})
	io.Copy(extractedData, bz2Reader)
	return bytes.NewReader(extractedData.Bytes())
}

func msg31(r *bytes.Reader) *Message31 {
	m31h := Message31Header{}
	binary.Read(r, binary.BigEndian, &m31h)

	m31 := Message31{
		Header:     m31h,
		MomentData: []interface{}{},
	}

	for i := uint16(0); i < m31h.DataBlockCount; i++ {
		d := DataBlock{}
		if err := binary.Read(r, binary.BigEndian, &d); err != nil {
			logrus.Panic(err.Error())
		}
		r.Seek(-4, io.SeekCurrent)

		// spew.Dump(d)

		blockName := string(d.DataName[:])
		// fmt.Printf("\t%s\n", blockName)
		switch blockName {
		case "VOL":
			d := VolumeData{}
			binary.Read(r, binary.BigEndian, &d)
			m31.MomentData = append(m31.MomentData, d)
		case "ELV":
			d := ElevationData{}
			binary.Read(r, binary.BigEndian, &d)
			m31.MomentData = append(m31.MomentData, d)
		case "RAD":
			d := RadialData{}
			binary.Read(r, binary.BigEndian, &d)
			m31.MomentData = append(m31.MomentData, d)
		case "REF":
			fallthrough
		case "VEL":
			fallthrough
		case "SW ":
			fallthrough
		case "ZDR":
			fallthrough
		case "PHI":
			fallthrough
		case "RHO":
			m := GenericDataMoment{}
			binary.Read(r, binary.BigEndian, &m)
			//LDM is the amount of space in bytes required for a data moment array and equals
			//((NG * DWS) / 8) where NG is the number of gates at the gate spacing resolution specified and DWS is the number of bits stored for each gate (DWS is always a multiple of 8).
			ldm := m.NumberDataMomentGates * uint16(m.DataWordSize) / 8
			data := make([]uint8, ldm)
			binary.Read(r, binary.BigEndian, &data)
			d := DataMoment{
				GenericDataMoment: m,
				Data:              data,
			}
			m31.MomentData = append(m31.MomentData, d)
		default:
			logrus.Panicf("Data Block - unknown type '%s'", blockName)
		}
	}
	return &m31
}