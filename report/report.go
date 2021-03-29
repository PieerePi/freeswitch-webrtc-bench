// The MIT License (MIT)
//
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
package report

import (
	"fmt"
	"time"
)

type CallReport struct {
	CallIndex     int      `json:"CallIndex"`
	Account       string   `json:"Account"`
	AccountIndex  int      `json:"AccountIndex"`
	RemoteAccount string   `json:"RemoteAccount"`
	CallStartTime JsonTime `json:"CallStartTime"`
	CallEndTime   JsonTime `json:"CallEndTime"`
	CallID        string   `json:"CallID"`
	CallResult    string   `json:"CallResult"`
	Logs          []string `json:"Logs"`
}

type JsonTime time.Time

func (jt JsonTime) MarshalJSON() ([]byte, error) {
	var stamp = fmt.Sprintf("\"%s\"", time.Time(jt).Format("2006-01-02 15:04:05"))
	return []byte(stamp), nil
}
