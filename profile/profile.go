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
package profile

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
)

// CallProfile is a json config file for UAC and UAS
type CallProfile struct {
	// "file" or "console"; the log file name looks like uac_benchlog_202103261437.log
	LogType string `json:"logtype"`
	// log directory
	LogDir string `json:"logdir"`
	// put call details in log
	CallDetailsInLog bool `json:"calldetailsinlog"`
	// separate call details file; the call details file name looks like uac_calldetails_202103261437.json
	CallDetailsFile bool `json:"calldetailsfile"`
	// put sdp in call details
	SdpInCallDetails bool `json:"sdpincalldetails"`

	// "0.0.0.0" or a specified IP; similar to '-i' of sipp
	LocalIP string `json:"localip"`
	// "0" or a specified port range which must be the same length as the accounts; similar to '-p' of sipp
	//
	// doesn't support all calls share one port
	LocalPorts         string   `json:"localports"`
	InternalLocalPorts []string // `json:"internallocalports,omitempty"`
	// "udp", "tcp", "tls", "ws" or "wss"; similar to '-t' of sipp
	Transport string `json:"transport"`

	ServerIP   string `json:"serverip"`
	ServerPort string `json:"serverport"`
	// comma separated ICE URLs
	ICEURLs       string `json:"iceurls"`
	ICEUsername   string `json:"iceusername"`
	ICECredential string `json:"icecredential"`

	// "uac" or "uas"; similar to '-sn' of sipp
	Role string `json:"role"`
	// an account range
	Accounts         string   `json:"accounts"`
	InternalAccounts []string // `json:"internalaccounts,omitempty"`
	// a password range, it should have the same length as the accounts
	Passwords         string   `json:"passwords"`
	InternalPasswords []string // `json:"internalpasswords,omitempty"`
	// for uas, it is ""
	//
	// for uac, it is a called party range, it should have the same length as the accounts
	CalledParties         string   `json:"calledparties"`
	InternalCalledParties []string // `json:"internalcalledparties,omitempty"`

	// maximum number of concurrent calls; similar to '-l' of sipp
	//
	// it can't be greater than the length of the accounts; if <= 0, it will be set to the length of the accounts
	MaxConcurrentCalls int `json:"maxconcurrentcalls"`
	// total number of processed calls; similar to '-m' of sipp
	//
	// after total calls, program exits; if <= 0, no totalcalls limitation
	TotalCalls int `json:"totalcalls"`
	// how long to send bye after the call is established, unit is millisecond
	//
	// if <= 0, no callduration limitation; similar to '-d' of sipp
	CallDuration int `json:"callduration"`
	// calls initiated every batch; similar to '-r' of sipp
	CallsPerBatch int `json:"callsperbatch"`
	// interval between every batch, unit is millisecond; similar to '-rp' of sipp
	BatchInterval int `json:"batchinterval"`
	// interval between every call, unit is millisecond
	//
	// callinterval * callsperbatch <= batchinterval
	CallInterval int `json:"callinterval"`

	// "audio" or "video" for uac
	//
	// the mediatype of uas depends on received SDP
	MediaType string `json:"mediatype"`
	// file path of the sending audio file
	//
	// it can be an absolute path or a relative path to the call profile
	AudioFile string `json:"audiofile"`
	// file path of the sending video file
	VideoFile string `json:"videofile"`
	// fps of the sending video file, default is 15
	VideoFileFps int `json:"videofilefps"`

	// "true" or "false"
	//
	// only the first call will dump the media
	DumpMedia bool `json:"dumpmedia"`
	// file path of the dumping audio file without suffix
	//
	// it can be an absolute path or a relative path to the call profile
	SavedAudioFile string `json:"savedaudiofile"`
	// file path of the dumping video file without suffix
	SavedVideoFile string `json:"savedvideofile"`
}

// ParseCallProfile parses the content of the file `filename` to the *CallProfile `cp`, or returns an error if something wrong.
func ParseCallProfile(filename string, cp *CallProfile) error {
	jsonbytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("ParseCallProfile parse json error: %v", err)
	}
	json.Unmarshal(jsonbytes, cp)
	if cp.LogType != "file" && cp.LogType != "console" {
		return fmt.Errorf("ParseCallProfile unknown logtype %q", cp.LogType)
	}
	if cp.InternalAccounts, err = parseRangePattern(cp.Accounts); err != nil {
		return fmt.Errorf("ParseCallProfile parse accounts error: %v", err)
	}
	if cp.InternalPasswords, err = parseRangePattern(cp.Passwords); err != nil {
		return fmt.Errorf("ParseCallProfile parse passwords error: %v", err)
	}
	if cp.Role == "uac" {
		if cp.InternalCalledParties, err = parseRangePattern(cp.CalledParties); err != nil {
			return fmt.Errorf("ParseCallProfile parse calledparties error: %v", err)
		}
	}
	if cp.LocalPorts == "" || cp.LocalPorts == "0" {
		cp.LocalPorts = fmt.Sprintf("0{%d}", len(cp.InternalAccounts))
	}
	if cp.InternalLocalPorts, err = parseRangePattern(cp.LocalPorts); err != nil {
		return fmt.Errorf("ParseCallProfile parse localports error: %v", err)
	}
	if len(cp.InternalAccounts) == 0 || len(cp.InternalAccounts) > len(cp.InternalPasswords) || (cp.Role == "uac" && len(cp.InternalAccounts) > len(cp.InternalCalledParties)) || len(cp.InternalAccounts) > len(cp.InternalLocalPorts) {
		return fmt.Errorf("ParseCallProfile localports-%d/accounts-%d/passwords-%d/calledparties-%d of unequal length",
			len(cp.InternalLocalPorts), len(cp.InternalAccounts), len(cp.InternalPasswords), len(cp.InternalCalledParties))
	}
	// log.Println(cp.InternalLocalPorts)
	// log.Println(cp.InternalAccounts)
	// log.Println(cp.InternalPasswords)
	// log.Println(cp.InternalCalledParties)
	if cp.Transport != "udp" && cp.Transport != "tcp" && cp.Transport != "tls" && cp.Transport != "ws" && cp.Transport != "wss" {
		return fmt.Errorf("ParseCallProfile unknown transport %q", cp.Transport)
	}
	if cp.Role != "uac" && cp.Role != "uas" {
		return fmt.Errorf("ParseCallProfile unknown role %q", cp.Role)
	}
	if cp.MaxConcurrentCalls <= 0 {
		cp.MaxConcurrentCalls = len(cp.InternalAccounts)
	}
	if cp.MaxConcurrentCalls > len(cp.InternalAccounts) {
		return fmt.Errorf("ParseCallProfile maxconcurrentcalls %d is greater than the length of accounts %d", cp.MaxConcurrentCalls, len(cp.InternalAccounts))
	}
	if cp.TotalCalls > 0 && cp.TotalCalls < cp.MaxConcurrentCalls {
		// don't start to many clients (uac/uas)
		cp.MaxConcurrentCalls = cp.TotalCalls
	}
	if cp.CallInterval*cp.CallsPerBatch > cp.BatchInterval {
		return fmt.Errorf("ParseCallProfile, %d(callinterval)*%d(callsperbatch)=%d should be <= %d(batchinterval)", cp.CallInterval, cp.CallsPerBatch, cp.CallInterval*cp.CallsPerBatch, cp.BatchInterval)
	}
	if cp.Role == "uac" {
		if cp.MediaType != "audio" && cp.MediaType != "video" {
			return fmt.Errorf("ParseCallProfile unknown mediatype %q", cp.MediaType)
		}
	}
	if cp.VideoFileFps <= 0 {
		cp.VideoFileFps = 15
	}
	return nil
}

// range pattern definition
//   "one,[one],one{repeat},one{@repeat},[one]{repeat},[one]{@repeat},
//    start-end,[start-end],start-end{repeat},start-end{@repeat},[start-end]{repeat},[start-end]{@repeat},
//    prefix[start-end]suffix,prefix[start-end]suffix{repeat},prefix[start-end]suffix{@repeat}"
//   [start-end]{repeat} means [start-end] ... [start-end]    (repeat times)
//   [start-end]{@repeat} means start{repeat} ... end{repeat}    (end-start+1 times)
func parseRangePattern(rp string) ([]string, error) {
	var ret []string
	var err error
	parts := strings.Split(rp, ",")
	for _, part := range parts {
		var repeat, start, end, prefix, suffix string
		var nrepeat, nstart, nend int
		var specialRepeatMode bool
		repeat = "1"
		if len(part) >= 1 && part[len(part)-1] == '}' {
			if bhead := strings.IndexByte(part, '{'); bhead != -1 {
				repeat = string(part[bhead+1 : len(part)-1])
				part = part[0:bhead]
			} else {
				return nil, fmt.Errorf("parseRangePattern: unmatched {}")
			}
		}
		if repeat != "" && repeat[0] == '@' {
			specialRepeatMode = true
			repeat = repeat[1:]
		}
		if nrepeat, err = strconv.Atoi(repeat); err != nil {
			return nil, fmt.Errorf("parseRangePattern: repeat %q is not an interger: %v", repeat, err)
		}
		sbhead := strings.IndexByte(part, '[')
		sbtail := strings.IndexByte(part, ']')
		if !((sbhead == -1 && sbtail == -1) || (sbhead != -1 && sbtail != -1 && sbtail > sbhead)) {
			return nil, fmt.Errorf("parseRangePattern: unmatched []")
		}
		if sbhead != -1 {
			prefix = part[0:sbhead]
			suffix = part[sbtail+1:]
			part = part[sbhead+1 : sbtail]
		}
		if part == "" {
			return nil, fmt.Errorf("parseRangePattern: empty start-end %q", part)
		}
		if strings.IndexByte(part, '{') != -1 || strings.IndexByte(part, '}') != -1 || strings.IndexByte(part, '[') != -1 || strings.IndexByte(part, ']') != -1 {
			return nil, fmt.Errorf("parseRangePattern: redundant {}[]")
		}
		numbers := strings.Split(part, "-")
		if len(numbers) == 2 {
			start = numbers[0]
			end = numbers[1]
		} else if len(numbers) == 1 {
			start = numbers[0]
			end = numbers[0]
		} else {
			return nil, fmt.Errorf("parseRangePattern: start-end %q has too much or zero -", part)
		}
		if start == "" || end == "" || (len(start) != len(end)) {
			return nil, fmt.Errorf("parseRangePattern: start %q or end %q is empty or of unequal length", start, end)
		}
		if start != end {
			if nstart, err = strconv.Atoi(start); err != nil {
				return nil, fmt.Errorf("parseRangePattern: start %q is not an interger: %v", start, err)
			}
			if nend, err = strconv.Atoi(end); err != nil {
				return nil, fmt.Errorf("parseRangePattern: end %q is not an interger: %v", end, err)
			}
			if nend < nstart {
				return nil, fmt.Errorf("parseRangePattern: end %q is less than start %q", end, start)
			}
		}
		if !specialRepeatMode { // [start-end]{repeat}
			numberFmt := fmt.Sprintf("%%0%dd", len(start))
			for i := 0; i < nrepeat; i++ {
				if start != end {
					for j := 0; j < (nend - nstart + 1); j++ {
						ret = append(ret, prefix+fmt.Sprintf(numberFmt, nstart+j)+suffix)
					}
				} else {
					ret = append(ret, prefix+start+suffix)
				}
			}
		} else { // [start-end]{@repeat}
			numberFmt := fmt.Sprintf("%%0%dd", len(start))
			if start != end {
				for i := 0; i < (nend - nstart + 1); i++ {
					for j := 0; j < nrepeat; j++ {
						ret = append(ret, prefix+fmt.Sprintf(numberFmt, nstart+i)+suffix)
					}
				}
			} else {
				for i := 0; i < nrepeat; i++ {
					ret = append(ret, prefix+start+suffix)
				}
			}
		}
	}
	return ret, nil
}
