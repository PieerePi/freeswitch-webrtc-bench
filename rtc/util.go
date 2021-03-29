// The MIT License (MIT)
//
// Copyright (c) 2021 Winlin
// Copyright (c) 2021 Pieere Pi
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
package rtc

import (
	"bytes"
	"strings"
	"time"

	"github.com/PieerePi/freeswitch-webrtc-bench/rtc/h264reader"
)

func packageAsSTAPA(frames ...*h264reader.NAL) *h264reader.NAL {
	first := frames[0]

	buf := bytes.Buffer{}
	buf.WriteByte(
		first.RefIdc<<5&0x60 | byte(24), // STAP-A
	)

	for _, frame := range frames {
		buf.WriteByte(byte(len(frame.Data) >> 8))
		buf.WriteByte(byte(len(frame.Data)))
		buf.Write(frame.Data)
	}

	return &h264reader.NAL{
		PictureOrderCount: first.PictureOrderCount,
		ForbiddenZeroBit:  false,
		RefIdc:            first.RefIdc,
		UnitType:          h264reader.NalUnitType(24), // STAP-A
		Data:              buf.Bytes(),
	}
}

type wallClock struct {
	start    time.Time
	duration time.Duration
}

func newWallClock() *wallClock {
	return &wallClock{start: time.Now()}
}

func (v *wallClock) Tick(d time.Duration) time.Duration {
	v.duration += d

	wc := time.Now().Sub(v.start)
	re := v.duration - wc
	if re > 30*time.Millisecond {
		return re
	}
	return 0
}

func getNthItemInALineOfSDP(s string, linePrefix string, n int) string {
	subIndex := strings.Index(s, linePrefix)
	if subIndex == -1 {
		return ""
	}
	linefeedIndex := strings.Index(s[subIndex:], "\r\n")
	if linefeedIndex == -1 {
		return ""
	}
	line := s[subIndex : subIndex+linefeedIndex]
	items := strings.Split(line, " ")
	if len(items) < n {
		return ""
	}
	return items[n-1]
}

func replaceNthItemInALineOfSDP(s string, linePrefix string, n int, new string) string {
	subIndex := strings.Index(s, linePrefix)
	if subIndex == -1 {
		return s
	}
	linefeedIndex := strings.Index(s[subIndex:], "\r\n")
	if linefeedIndex == -1 {
		return s
	}
	line := s[subIndex : subIndex+linefeedIndex]
	items := strings.Split(line, " ")
	if len(items) < n {
		return s
	}
	var ret string = s[0:subIndex]
	for i := 0; i < len(items); i++ {
		if i+1 == n {
			ret = ret + new
		} else {
			ret = ret + items[i]
		}
		if i != len(items)-1 {
			ret = ret + " "
		}
	}
	ret = ret + s[subIndex+linefeedIndex:]
	return ret
}

func removeNthLineOfSDP(s string, linePrefix string, n int) string {
	var subIndex int
	var curIndex int
	for i := 0; i < n; i++ {
		subIndex = strings.Index(s[curIndex:], linePrefix)
		if subIndex == -1 {
			return s
		}
		if i != (n - 1) {
			curIndex = curIndex + subIndex + len(linePrefix)
		}
	}

	linefeedIndex := strings.Index(s[curIndex+subIndex:], "\r\n")
	if linefeedIndex == -1 {
		return s
	}
	return s[0:curIndex+subIndex] + s[curIndex+subIndex+linefeedIndex+2:]
}
