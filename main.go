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
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/PieerePi/freeswitch-webrtc-bench/profile"
	"github.com/PieerePi/freeswitch-webrtc-bench/report"
	"github.com/PieerePi/freeswitch-webrtc-bench/sip"
)

func main() {
	var profilePath string
	flag.StringVar(&profilePath, "p", "", "")
	flag.Parse()
	if profilePath == "" {
		log.Fatalf("Usage: %v -p \"call profile file path\"", os.Args[0])
	}
	var cp profile.CallProfile
	if err := profile.ParseCallProfile(profilePath, &cp); err != nil {
		log.Fatalf("parse call profile %q error: %v", profilePath, err)
	}

	if cp.LogType == "file" {
		logDir := cp.LogDir
		if logDir != "" && logDir[len(logDir)-1] != os.PathSeparator {
			logDir += string(os.PathSeparator)
		}
		logFileName := logDir + cp.Role + "_benchlog_" + time.Now().Format("20060102150405") + ".log"
		if logFile, err := os.Create(logFileName); err != nil {
			log.Fatalf("create log file %q error: %v", logFileName, err)
		} else {
			defer logFile.Close()
			log.SetOutput(logFile)
			profileContent, _ := ioutil.ReadFile(profilePath)
			log.Printf("The call profile content is:\n%s", profileContent)
		}
	}

	var cdf *os.File
	if cp.CallDetailsFile {
		var err error
		logDir := cp.LogDir
		if logDir != "" && logDir[len(logDir)-1] != os.PathSeparator {
			logDir += string(os.PathSeparator)
		}
		callDetailsFileName := logDir + cp.Role + "_calldetails_" + time.Now().Format("20060102150405") + ".json"
		if cdf, err = os.Create(callDetailsFileName); err != nil {
			log.Fatalf("create call details file %q error: %v", callDetailsFileName, err)
		} else {
			defer func() {
				if curPos, _ := cdf.Seek(0, 1); curPos == 2 {
					cdf.Seek(-1, 2)
				} else {
					cdf.Seek(-2, 2)
				}
				cdf.WriteString("\n]")
				cdf.Close()
			}()
			cdf.WriteString("[\n")
		}
	}

	if cp.Role == "uac" {
		uac(&cp, cdf)
	} else {
		uas(&cp, cdf)
	}
}

func uac(cp *profile.CallProfile, cdf *os.File) {
	mainCtx, mainCancel := context.WithCancel(context.Background())
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
		for sig := range sigs {
			log.Printf("Quit for signal %v", sig)
			time.Sleep(1 * time.Second)
			mainCancel()
		}
	}()

	var startCallChan []chan int
	var callEndChan = make(chan report.CallReport, cp.MaxConcurrentCalls)

	var inCall = make([]bool, cp.MaxConcurrentCalls, cp.MaxConcurrentCalls)
	var idleUac = cp.MaxConcurrentCalls
	var startedCalls, completedCalls, okCalls, badCalls, unknownCalls int
	var startedAllCallsPrinted bool
	var idleUacZeroPrinted bool

	var wg sync.WaitGroup
	var i, j int

	// start uac
	for i = 0; i < cp.MaxConcurrentCalls; i++ {
		wg.Add(1)
		startCallChan = append(startCallChan, make(chan int, 1))
		go func(n int) {
			defer wg.Done()
			sip.UA(mainCtx, cp, n, startCallChan[n], callEndChan)
		}(i)
	}

uacmainloop:
	for {
		for i = 0; i < cp.CallsPerBatch; i++ {
			select {
			case <-mainCtx.Done():
				// log.Println("mainCtx.Done(), break uacmainloop")
				break uacmainloop
			case cr := <-callEndChan:
				eccLen := len(callEndChan)
				for {
					if inCall[cr.AccountIndex] {
						inCall[cr.AccountIndex] = false
						idleUac++
						idleUacZeroPrinted = false
						completedCalls++
						cr.AccountIndex++
						if cdf != nil {
							if jsonbytes, err := json.MarshalIndent(cr, "", "    "); err != nil {
								log.Printf("write call details error: %v", err)
							} else {
								cdf.Write(jsonbytes)
								cdf.WriteString(",\n")
							}
						}
						switch cr.CallResult {
						case "Successful":
							okCalls++
						case "Failed":
							badCalls++
						case "":
							unknownCalls++
						}
					} else {
						log.Printf("What's wrong, [%s] not in call?", cr.Account)
					}
					if eccLen > 0 {
						cr = <-callEndChan
						eccLen--
						continue
					}
					break
				}
			default:
			}

			if cp.TotalCalls > 0 && completedCalls >= cp.TotalCalls {
				log.Println("All calls completed, let all UACs exit")
				mainCancel()
				break uacmainloop
			} else if cp.TotalCalls > 0 && startedCalls >= cp.TotalCalls {
				if !startedAllCallsPrinted {
					log.Println("Finish starting all calls and wait for all calls to finish...")
					startedAllCallsPrinted = true
				}
				time.Sleep(100 * time.Millisecond)
				i--
				continue
			} else if idleUac == 0 {
				if !idleUacZeroPrinted {
					idleUacZeroPrinted = true
					// log.Println("All UACs are busy, wait for an idle UA to start new call...")
				}
				time.Sleep(100 * time.Millisecond)
				i--
				continue
			}

			for {
				if !inCall[j%cp.MaxConcurrentCalls] {
					break
				}
				j++
			}
			inCall[j%cp.MaxConcurrentCalls] = true
			idleUac--
			startCallChan[j%cp.MaxConcurrentCalls] <- (startedCalls + 1)
			startedCalls++
			j++
			j %= cp.MaxConcurrentCalls
			time.Sleep(time.Duration(cp.BatchInterval/cp.CallsPerBatch) * time.Millisecond)
		}
	}

	log.Println("Wait for UACs to exit...")
	wg.Wait()
	log.Println("Call summary:")
	log.Printf("\tStarted calls: %d", startedCalls)
	log.Printf("\tCompleted calls: %d", completedCalls)
	log.Printf("\tSuccessful calls: %d", okCalls)
	log.Printf("\tFailed calls: %d", badCalls)
	log.Printf("\tUnknown calls: %d", unknownCalls)
	log.Println("All done.")
}

func uas(cp *profile.CallProfile, cdf *os.File) {
	mainCtx, mainCancel := context.WithCancel(context.Background())
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
		for sig := range sigs {
			log.Printf("Quit for signal %v", sig)
			time.Sleep(1 * time.Second)
			mainCancel()
		}
	}()

	var callEndChan = make(chan report.CallReport, cp.MaxConcurrentCalls)

	var completedCalls, okCalls, badCalls, unknownCalls int

	var wg sync.WaitGroup
	var i int

	// start uas
	for i = 0; i < cp.MaxConcurrentCalls; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sip.UA(mainCtx, cp, n, nil, callEndChan)
		}(i)
	}

uasmainloop:
	for {
		select {
		case <-mainCtx.Done():
			// log.Println("mainCtx.Done(), break uasmainloop")
			break uasmainloop
		case cr := <-callEndChan:
			eccLen := len(callEndChan)
			for {
				completedCalls++
				cr.AccountIndex++
				switch cr.CallResult {
				case "Successful":
					okCalls++
				case "Failed":
					badCalls++
				case "":
					unknownCalls++
				}
				if cdf != nil {
					if jsonbytes, err := json.MarshalIndent(cr, "", "    "); err != nil {
						log.Printf("write call details error: %v", err)
					} else {
						cdf.Write(jsonbytes)
						cdf.WriteString(",\n")
					}
				}
				if eccLen > 0 {
					cr = <-callEndChan
					eccLen--
					continue
				}
				break
			}
			if cp.TotalCalls > 0 && completedCalls >= cp.TotalCalls {
				log.Println("All calls completed, let all UASs exit")
				mainCancel()
				break uasmainloop
			}
		}
	}

	log.Println("Wait for UASs to exit...")
	wg.Wait()
	log.Println("Call summary:")
	log.Printf("\tCompleted calls: %d", completedCalls)
	log.Printf("\tSuccessful calls: %d", okCalls)
	log.Printf("\tFailed calls: %d", badCalls)
	log.Printf("\tUnknown calls: %d", unknownCalls)
	log.Println("All done.")
}
