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
package sip

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/PieerePi/freeswitch-webrtc-bench/chans"
	"github.com/PieerePi/freeswitch-webrtc-bench/profile"
	"github.com/PieerePi/freeswitch-webrtc-bench/report"
	"github.com/PieerePi/freeswitch-webrtc-bench/rtc"
	"github.com/cloudwebrtc/go-sip-ua/pkg/account"
	"github.com/cloudwebrtc/go-sip-ua/pkg/session"
	"github.com/cloudwebrtc/go-sip-ua/pkg/stack"
	"github.com/cloudwebrtc/go-sip-ua/pkg/ua"
	gosiplog "github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
)

var (
	mylogger          gosiplog.Logger
	uasCallIndex      int
	uasCallIndexMutex sync.Mutex
)

func init() {
	mylogger = gosiplog.NewDefaultLogrusLogger().WithPrefix("Client")
	// TODO: log sip warn/error to file
	// mylogger.SetLevel(gosiplog.InfoLevel)
	mylogger.SetLevel(gosiplog.WarnLevel)
}

func UA(mainCtx context.Context, cp *profile.CallProfile, accountIndex int, startCallChan <-chan int, callEndChan chan<- report.CallReport) {
	stack := stack.NewSipStack(&stack.SipStackConfig{Extensions: []string{"replaces", "outbound"}, Dns: "8.8.8.8"}, mylogger)
	if err := stack.ListenTLS("wss", cp.LocalIP+":"+cp.InternalLocalPorts[accountIndex], nil); err != nil {
		mylogger.Error(err)
		stack.Shutdown()
		return
	}

	ua := ua.NewUserAgent(&ua.UserAgentConfig{
		UserAgent: "Go Sip Client/1.0.0",
		SipStack:  stack,
	}, mylogger)

	var registerState account.RegisterState
	ua.RegisterStateHandler = func(state account.RegisterState) {
		registerState = state
		mylogger.Infof("RegisterStateHandler: user => %s, state => %v, expires => %v", state.Account.AuthInfo.AuthUser, state.StatusCode, state.Expiration)
	}

	uri, err := parser.ParseUri("sip:" + cp.InternalAccounts[accountIndex] + "@" + cp.ServerIP + ";transport=" + cp.Transport)
	if err != nil {
		mylogger.Error(err)
	}

	profile := account.NewProfile(uri.Clone(), "goSIP",
		&account.AuthInfo{
			AuthUser: cp.InternalAccounts[accountIndex],
			Password: cp.InternalPasswords[accountIndex],
			Realm:    cp.ServerIP,
		},
		1800,
		stack,
	)

	recipient, err := parser.ParseSipUri("sip:" + cp.ServerIP + ":" + cp.ServerPort + ";transport=" + cp.Transport)
	if err != nil {
		mylogger.Error(err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		ua.SendRegister(profile, recipient, profile.Expires, nil)
	}()
	time.Sleep(3 * time.Second)
	if registerState.StatusCode != 200 {
		log.Printf("[%s] UA: register failed, start failed", cp.InternalAccounts[accountIndex])
		ua.Shutdown()
		return
	} else {
		log.Printf("[%s] UA: register successfully", cp.InternalAccounts[accountIndex])
	}

	var cr report.CallReport
	cr.AccountIndex = accountIndex
	cr.Account = cp.InternalAccounts[accountIndex]
	if cp.Role == "uac" {
		cr.RemoteAccount = cp.InternalCalledParties[accountIndex]
	}
	var lh = &logHelper{cp: cp, cr: &cr}
	sdpToMediaChan := make(chan chans.SessionDescription, 1)
	sdpFromMediaChan := make(chan chans.SessionDescription, 1)
	callConfirmedChan := make(chan *session.Session, 1)
	callReceivedChan := make(chan *session.Session, 1)
	callTerminatedChan := make(chan *session.Session, 1)
	logChan := make(chan string, 50)
	// logChan := make(chan string)

	ua.InviteStateHandler = func(sess *session.Session, req *sip.Request, resp *sip.Response, state session.Status) {
		mylogger.Infof("InviteStateHandler: state => %v, type => %s", state, sess.Direction())

		switch state {
		case session.InviteReceived:
			// for callee, invite received
			if cp.Role == "uas" {
				if len(sdpToMediaChan) != 0 {
					lh.logf("ua: sdpToMediaChan is not empty %d? ua will block", len(sdpToMediaChan))
				}
				// set sdp to pion(remote offer)
				sdpToMediaChan <- chans.SessionDescription{Type: "offer", SDP: (*req).Body()}
				// notify ua
				callReceivedChan <- sess
			} else {
				// uac rejects incoming call to keep logic sanity
				sess.End()
			}
		case session.Confirmed:
			// for caller, 200 ok (invite) received; for callee, ack received
			if cp.Role == "uac" {
				if len(sdpToMediaChan) != 0 {
					lh.logf("ua: sdpToMediaChan is not empty %d? ua will block", len(sdpToMediaChan))
				}
				// set sdp to pion(remote answer)
				sdpToMediaChan <- chans.SessionDescription{Type: "answer", SDP: (*resp).Body()}
			}
			// notify ua
			callConfirmedChan <- sess
		// pion can't handle pranswer correctly?
		// case session.EarlyMedia:
		// 	if cp.Role == "uac" {
		// 		if len(sdpToMediaChan) != 0 {
		// 			lh.logf("ua: sdpToMediaChan is not empty %d? ua will block", len(sdpToMediaChan))
		// 		}
		// 		// set sdp to pion(remote answer)
		// 		sdpToMediaChan <- chans.SessionDescription{Type: "pranswer", SDP: (*resp).Body()}
		// 	}
		case session.Canceled:
			fallthrough
		case session.Failure:
			fallthrough
		case session.Terminated:
			// for caller, 200 ok (bye) received; for callee, bye received
			cr.CallEndTime = report.JsonTime(time.Now())
			if state == session.Failure {
				lh.logln("sip: call failed?")
				cr.CallResult = "Failed"
			} else {
				cr.CallResult = "Successful"
			}
			// notify ua
			callTerminatedChan <- sess
		}
	}

	var called sip.Uri
	if cp.Role == "uac" {
		called, err = parser.ParseUri("sip:" + cp.InternalCalledParties[accountIndex] + "@" + cp.ServerIP)
		if err != nil {
			mylogger.Error(err)
		}
	}

	var currentSession *session.Session
	var mediaCtx context.Context = context.TODO()
	var mediaCancel context.CancelFunc
	mediaFuncEndChan := make(chan struct{}, 1)
	var callDurationCtx = context.TODO()
	var callDurationCancel context.CancelFunc
	var hangupByMe bool

uamainloop:
	for {
		select {
		case <-mainCtx.Done():
			// TODO: how to get call details?
			// log.Printf("[%s] got mainCtx.Done()", cr.Account)
			if currentSession != nil {
				// log.Println("currentSession.End()")
				currentSession.End()
			}
			if mediaCancel != nil {
				// log.Println("mediaCancel()")
				mediaCancel()
				<-mediaFuncEndChan
				for logLen := len(logChan); logLen > 0; logLen-- {
					log := <-logChan
					lh.logf("     rtc: %s", log)
				}
			}
			if callDurationCancel != nil {
				// log.Println("callDurationCancel()")
				callDurationCancel()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				ua.SendRegister(profile, recipient, 0, nil)
				ua.Shutdown()
				log.Printf("[%s] UA: unregister done", cr.Account)
			}()
			break uamainloop

		// only for uac
		case callIndex := <-startCallChan:
			if currentSession != nil || mediaCtx != context.TODO() || mediaCancel != nil {
				// this will never happen
				log.Printf("[%s] already on a call?", cr.Account)
				break
			}
			cr.CallIndex = callIndex
			cr.CallStartTime = report.JsonTime(time.Now())
			cr.CallEndTime = report.JsonTime(time.Now())
			cr.CallID = ""
			cr.CallResult = ""
			cr.Logs = []string{}
			lh.logf("ua: start %dth call", cr.CallIndex)
			log.Printf("[%s] UA: start %dth call, to %s", cr.Account, cr.CallIndex, cr.RemoteAccount)
			mediaCtx, mediaCancel = context.WithCancel(mainCtx)
			wg.Add(1)
			go func() {
				defer wg.Done()
				var record bool
				if cr.CallIndex == 1 {
					record = true
				}
				if err := rtc.MediaFunc(mediaCtx, cp, sdpFromMediaChan, sdpToMediaChan, logChan, mediaFuncEndChan, record, false, false); err != nil {
					lh.logf("     rtc: returns err %v", err)
				}
			}()

		case sdp := <-sdpFromMediaChan:
			if sdp.Type == "offer" {
				lh.logln("sip: start to invite")
				currentSession, _ = ua.Invite(profile, called, recipient, &sdp.SDP)
				if currentSession != nil {
					cr.CallStartTime = report.JsonTime(time.Now())
					cr.CallID = currentSession.CallID().Value()
					lh.logf("sip: Call-ID is %s", cr.CallID)
				} else {
					// TODO: error handling
				}
			} else if sdp.Type == "answer" {
				lh.logln("sip: start to answer")
				if currentSession != nil {
					currentSession.ProvideAnswer(sdp.SDP)
					currentSession.Accept(200)
				}
			}

		case <-callConfirmedChan:
			if cp.CallDuration > 0 {
				if callDurationCtx != context.TODO() || callDurationCancel != nil {
					// we always get two callConfirmedChans for uac
					// lh.logln("sip: already have a call duration timer?")
					break
				}
				lh.logln("sip: call confirmed, call duration timer created")
				callDurationCtx, callDurationCancel = context.WithTimeout(mainCtx, time.Duration(cp.CallDuration)*time.Millisecond)
			} else {
				lh.logln("sip: call confirmed")
			}

		case sess := <-callReceivedChan:
			uasCallIndexMutex.Lock()
			uasCallIndex++
			callIndex := uasCallIndex
			caller := ""
			if from, ok := sess.Request().From(); ok {
				caller = from.Address.User().String()
			}
			uasCallIndexMutex.Unlock()
			if currentSession != nil {
				if currentSession == sess {
					lh.logln("sip: got duplicated session?")
				} else if sess != nil && currentSession.CallID().Value() == sess.CallID().Value() {
					// this will never happen
					lh.logln("sip: got duplicated session with same callid?")
				} else {
					log.Printf("[%s] UA: received a new call from %s while calling? hang up it", cr.Account, caller)
					var newcr report.CallReport = cr
					newcr.CallIndex = callIndex
					newcr.RemoteAccount = caller
					newcr.CallStartTime = report.JsonTime(time.Now())
					newcr.CallEndTime = report.JsonTime(time.Now())
					newcr.CallID = sess.CallID().Value()
					newcr.CallResult = "Failed"
					newcr.Logs = []string{fmt.Sprintf("%v UA: received a new call from %s while calling? hang up it", time.Now().Format("2006-01-02 15:04:05"), caller)}
					sess.End()
					callEndChan <- newcr
				}
				break
			}
			cr.CallIndex = callIndex
			cr.RemoteAccount = caller
			cr.CallStartTime = report.JsonTime(time.Now())
			cr.CallEndTime = report.JsonTime(time.Now())
			cr.CallID = sess.CallID().Value()
			cr.CallResult = ""
			cr.Logs = []string{}
			lh.logf("ua: received %dth call", cr.CallIndex)
			log.Printf("[%s] UA: received %dth call, from %s", cr.Account, cr.CallIndex, cr.RemoteAccount)
			lh.logf("sip: Call-ID is %s", cr.CallID)
			currentSession = sess
			mediaCtx, mediaCancel = context.WithCancel(mainCtx)
			wg.Add(1)
			go func() {
				defer wg.Done()
				var record bool
				if cr.CallIndex == 1 {
					record = true
				}
				if err := rtc.MediaFunc(mediaCtx, cp, sdpFromMediaChan, sdpToMediaChan, logChan, mediaFuncEndChan, record, false, false); err != nil {
					lh.logf("     rtc: returns err %v", err)
				}
			}()

		case sess := <-callTerminatedChan:
			if currentSession == nil {
				log.Printf("[%s] sip: call terminated, but currentSession is already nil?", cr.Account)
				break
			}
			lh.logln("sip: call terminated")
			log.Printf("[%s] UA: %dth call terminated, with %s, result %s", cr.Account, cr.CallIndex, cr.RemoteAccount, cr.CallResult)
			if currentSession != sess {
				lh.logln("sip: currentSession != sess, not my current session?")
				break
			}
			currentSession = nil
			if mediaCancel != nil {
				mediaCancel()
				<-mediaFuncEndChan
				for logLen := len(logChan); logLen > 0; logLen-- {
					log := <-logChan
					lh.logf("     rtc: %s", log)
				}
				mediaCtx = context.TODO()
				mediaCancel = nil
				// clear sdpToMediaChan (uac always gets two callConfirmedChans, there's one left after call)
				for stmcLen := len(sdpToMediaChan); stmcLen > 0; stmcLen-- {
					<-sdpToMediaChan
				}
			}
			if callDurationCancel != nil {
				callDurationCancel()
				callDurationCtx = context.TODO()
				callDurationCancel = nil
			}
			if cp.Role == "uac" {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// let the callee have time to do clean work
					if hangupByMe {
						// hangup by me due to call duration timeout
						lh.logln("ua: wait 5 seconds for remote cleaning")
						time.Sleep(5 * time.Second)
						hangupByMe = false
					}
					callEndChan <- cr
				}()
			} else {
				callEndChan <- cr
			}

		// do it in callTerminatedChan
		// case <-mediaCtx.Done():
		// 	mediaCtx = context.TODO()
		// 	mediaCancel = nil

		case <-callDurationCtx.Done():
			lh.logln("sip: call duration reached, timeout")
			if currentSession != nil {
				hangupByMe = true
				lh.logln("sip: hang up the current session")
				currentSession.End()
			}
			// should set callDurationCtx to context.TODO() immediately
			callDurationCtx = context.TODO()
			callDurationCancel = nil

		case log := <-logChan:
			lh.logf("     rtc: %s", log)
		}
	}

	// log.Printf("[%s] wait for all workers done", cr.Account)
	wg.Wait()
	log.Printf("[%s] UA: exits", cr.Account)
}

type logHelper struct {
	cp *profile.CallProfile
	cr *report.CallReport
}

func (lh *logHelper) logf(format string, v ...interface{}) {
	if lh.cp.CallDetailsFile {
		// prefix := fmt.Sprintf("%v [%s-%s] ", time.Now().Format("2006-01-02 15:04:05"), lh.cr.Account, lh.cr.RemoteAccount)
		prefix := fmt.Sprintf("%v ", time.Now().Format("2006-01-02 15:04:05"))
		lh.cr.Logs = append(lh.cr.Logs, prefix+fmt.Sprintf(format, v...))
	}
	if lh.cp.CallDetailsInLog {
		prefix := fmt.Sprintf("[%s-%s] ", lh.cr.Account, lh.cr.RemoteAccount)
		log.Println(prefix + fmt.Sprintf(format, v...))
	}
}

func (lh *logHelper) logln(v ...interface{}) {
	if lh.cp.CallDetailsFile {
		// prefix := fmt.Sprintf("%v [%s-%s] ", time.Now().Format("2006-01-02 15:04:05"), lh.cr.Account, lh.cr.RemoteAccount)
		prefix := fmt.Sprintf("%v ", time.Now().Format("2006-01-02 15:04:05"))
		lh.cr.Logs = append(lh.cr.Logs, prefix+fmt.Sprintf("%s", v...))
	}
	if lh.cp.CallDetailsInLog {
		prefix := fmt.Sprintf("[%s-%s] ", lh.cr.Account, lh.cr.RemoteAccount)
		log.Println(prefix + fmt.Sprintf("%s", v...))
	}
}
