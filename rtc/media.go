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
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/PieerePi/freeswitch-webrtc-bench/chans"
	"github.com/PieerePi/freeswitch-webrtc-bench/profile"
	"github.com/PieerePi/freeswitch-webrtc-bench/rtc/g711writer"
	"github.com/PieerePi/freeswitch-webrtc-bench/rtc/h264writer"
	"github.com/ossrs/go-oryx-lib/errors"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

// @see https://github.com/pion/webrtc/blob/master/examples/play-from-disk/main.go
func MediaFunc(ctx context.Context, cp *profile.CallProfile, sdpToUAChan chan<- chans.SessionDescription, sdpFromUAChan <-chan chans.SessionDescription, logChan chan<- string, mediaFuncEndChan chan<- struct{}, record bool, enableAudioLevel, enableTWCC bool) error {
	var lh = &logHelper{logChan: logChan}

	// Wait for event from context or tracks.
	var wg sync.WaitGroup

	doNotifyMediaFuncExit := func() {
		wg.Wait()
		lh.logln("MediaFunc exits")
		mediaFuncEndChan <- struct{}{}
	}
	defer doNotifyMediaFuncExit()

	var remoteOffer, remoteAnswer chans.SessionDescription
	var localOffer, localAnswer webrtc.SessionDescription
	var callKind string

	var sourceAudio, sourceVideo string
	var fps int

	if cp.Role == "uac" {
		if cp.MediaType == "video" {
			callKind = "video"
			sourceVideo = cp.VideoFile
			fps = cp.VideoFileFps
		} else if cp.MediaType == "audio" {
			callKind = "audio"
			sourceAudio = cp.AudioFile
		}
	} else {
		select {
		case <-ctx.Done():
			return nil
		case remoteOffer = <-sdpFromUAChan:
			if remoteOffer.Type != "offer" {
				lh.logln("Got offer")
				lh.logln("SDP type from sdpFromUAChan is not offer?")
				return nil
			}
			// Prefer video
			if strings.Index(remoteOffer.SDP, "m=video") != -1 {
				callKind = "video"
				sourceVideo = cp.VideoFile
				fps = cp.VideoFileFps
				audioStart := strings.Index(remoteOffer.SDP, "m=audio")
				videoStart := strings.Index(remoteOffer.SDP, "m=video")
				// Remove audio section
				if audioStart != -1 {
					if audioStart < videoStart {
						remoteOffer.SDP = remoteOffer.SDP[0:audioStart] + remoteOffer.SDP[videoStart:]
					} else {
						remoteOffer.SDP = remoteOffer.SDP[0:audioStart]
					}
				}
			} else if strings.Index(remoteOffer.SDP, "m=audio") != -1 {
				callKind = "audio"
				sourceAudio = cp.AudioFile
			}
			// // Insert a=group:BUNDLE 0
			// amsidStart := strings.Index(remoteOffer.SDP, "a=msid-semantic")
			// if amsidStart != -1 {
			// 	remoteOffer.SDP = remoteOffer.SDP[0:amsidStart] + "a=group:BUNDLE 0\r\n" + remoteOffer.SDP[amsidStart:]
			// }
			// Insert a=sendrecv
			remoteOffer.SDP = remoteOffer.SDP + "a=sendrecv\r\n"
			// Insert a=mid:0
			remoteOffer.SDP = remoteOffer.SDP + "a=mid:0\r\n"
			if cp.SdpInCallDetails {
				lh.logf("Got offer\n%s", remoteOffer.SDP)
			} else {
				lh.logln("Got offer")
			}
		}
	}

	// lh.logf("Start to call audio=%v, video=%v, fps=%v, audio-level=%v, twcc=%v",
	// 	sourceAudio, sourceVideo, fps, enableAudioLevel, enableTWCC)

	// Filter for SPS/PPS marker.
	var aIngester *audioIngester
	var vIngester *videoIngester

	var da_g711u media.Writer
	var da_opus media.Writer
	var dv_h264 media.Writer

	var receivers []*webrtc.RTPReceiver

	var pc *webrtc.PeerConnection
	var err2 error

	doClosed := false
	doClose := func() {
		if doClosed {
			return
		}
		doClosed = true
		// lh.logln("doClose()")
		if pc != nil {
			pc.Close()
		}
		if vIngester != nil {
			vIngester.Close()
		}
		if aIngester != nil {
			aIngester.Close()
		}
		if da_g711u != nil {
			da_g711u.Close()
		}
		if da_opus != nil {
			da_opus.Close()
		}
		if dv_h264 != nil {
			dv_h264.Close()
		}
		for _, receiver := range receivers {
			receiver.Stop()
		}
	}
	defer doClose()

	// For audio-level and sps/pps marker.
	webrtcNewPeerConnection := func(configuration webrtc.Configuration) (*webrtc.PeerConnection, error) {
		m := &webrtc.MediaEngine{}
		// Only support h264/g711u/opus
		// if err := m.RegisterDefaultCodecs(); err != nil {
		if err := registerDefaultCodecs(m, cp.AudioFile); err != nil {
			return nil, err
		}

		for _, extension := range []string{sdp.SDESMidURI, sdp.SDESRTPStreamIDURI, sdp.TransportCCURI} {
			if extension == sdp.TransportCCURI && !enableTWCC {
				continue
			}
			if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: extension}, webrtc.RTPCodecTypeVideo); err != nil {
				return nil, err
			}
		}

		// https://github.com/pion/ion/issues/130
		// https://github.com/pion/ion-sfu/pull/373/files#diff-6f42c5ac6f8192dd03e5a17e9d109e90cb76b1a4a7973be6ce44a89ffd1b5d18R73
		for _, extension := range []string{sdp.SDESMidURI, sdp.SDESRTPStreamIDURI, sdp.AudioLevelURI} {
			if extension == sdp.AudioLevelURI && !enableAudioLevel {
				continue
			}
			if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: extension}, webrtc.RTPCodecTypeAudio); err != nil {
				return nil, err
			}
		}

		registry := &interceptor.Registry{}
		if err := webrtc.RegisterDefaultInterceptors(m, registry); err != nil {
			return nil, err
		}

		if sourceAudio != "" {
			aIngester = NewAudioIngester(sourceAudio)
			registry.Add(aIngester.audioLevelInterceptor)
		}
		if sourceVideo != "" {
			vIngester = NewVideoIngester(sourceVideo)
			registry.Add(vIngester.markerInterceptor)
		}

		api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(registry))
		return api.NewPeerConnection(configuration)
	}

	if cp.ICEURLs != "" {
		pc, err2 = webrtcNewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{
			{
				URLs:           strings.Split(cp.ICEURLs, ","),
				Username:       cp.ICEUsername,
				Credential:     cp.ICECredential,
				CredentialType: webrtc.ICECredentialTypePassword,
			},
		},
			ICETransportPolicy: webrtc.ICETransportPolicyRelay,
		})
	} else {
		pc, err2 = webrtcNewPeerConnection(webrtc.Configuration{})
	}

	if err2 != nil {
		return errors.Wrapf(err2, "Create PC")
	}

	if vIngester != nil {
		if err := vIngester.AddTrack(pc, fps); err != nil {
			return errors.Wrapf(err, "Add track")
		}
	}

	if aIngester != nil {
		if err := aIngester.AddTrack(pc); err != nil {
			return errors.Wrapf(err, "Add track")
		}
	}

	// ICE timeout in 5 seconds if have no candidates
	iceCtx, iceCancel := context.WithTimeout(ctx, 5*time.Second)
	// iceCtx, iceCancel := context.WithCancel(ctx)
	// gatherComplete := webrtc.GatheringCompletePromise(pc)
	// pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
	// 	if state == webrtc.ICEGathererStateComplete {
	// 		lh.logln("ICE gathering done")
	// 		iceCancel()
	// 	}
	// })
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			lh.logln("ICE gathering done")
			iceCancel()
		} else {
			lh.logf("ICE gathering got one %v", candidate)
			// // Only one media, done!
			// iceCancel()
		}
	})

	// ICE state management.
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		lh.logf("ICE state %v", state)
	})

	pc.OnSignalingStateChange(func(state webrtc.SignalingState) {
		lh.logf("Signaling state %v", state)
	})

	if vIngester != nil {
		vIngester.sVideoSender.Transport().OnStateChange(func(state webrtc.DTLSTransportState) {
			lh.logf("Video DTLS state %v", state)
		})
	}

	if aIngester != nil {
		aIngester.sAudioSender.Transport().OnStateChange(func(state webrtc.DTLSTransportState) {
			lh.logf("Audio DTLS state %v", state)
		})
	}

	ctx, cancel := context.WithCancel(ctx)
	pcDone, pcDoneCancel := context.WithCancel(context.Background())
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		lh.logf("PC state %v", state)

		if state == webrtc.PeerConnectionStateConnected {
			pcDoneCancel()
		}

		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			if ctx.Err() != nil {
				return
			}

			lh.logf("Close for PC state %v", state)
			cancel()
		}
	})

	handleTrack := func(ctx context.Context, track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) error {
		var err3 error

		if !record {
			return nil
		}

		// Send a PLI on an interval so that the publisher is pushing a keyframe
		pli := 10
		wg.Add(1)
		go func() {
			defer func() {
				if track.Kind() != webrtc.RTPCodecTypeAudio && pli > 0 {
					lh.logln("Pli sender exits")
				}
				wg.Done()
			}()

			if track.Kind() == webrtc.RTPCodecTypeAudio {
				return
			}

			if pli <= 0 {
				return
			}

			// Send a fir first
			_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.FullIntraRequest{
				MediaSSRC: uint32(track.SSRC()),
			}})

			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Duration(pli) * time.Second):
					_ = pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{
						MediaSSRC: uint32(track.SSRC()),
					}})
				}
			}
		}()

		receivers = append(receivers, receiver)

		codec := track.Codec()

		trackDesc := fmt.Sprintf("channels=%v", codec.Channels)
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			trackDesc = fmt.Sprintf("fmtp=%v", codec.SDPFmtpLine)
		}
		if headers := receiver.GetParameters().HeaderExtensions; len(headers) > 0 {
			trackDesc = fmt.Sprintf("%v, header=%v", trackDesc, headers)
		}
		lh.logf("Got track %v, pt=%v, tbn=%v, %v",
			codec.MimeType, codec.PayloadType, codec.ClockRate, trackDesc)

		if codec.MimeType == "audio/opus" {
			if da_opus == nil && cp.SavedAudioFile != "" {
				if da_opus, err3 = oggwriter.New(cp.SavedAudioFile+".ogg", codec.ClockRate, codec.Channels); err3 != nil {
					return errors.Wrapf(err3, "New audio dumper")
				}
				lh.logf("Open ogg writer file=%v, tbn=%v, channels=%v",
					cp.SavedAudioFile+".ogg", codec.ClockRate, codec.Channels)
			}

			if err3 = writeTrackToDisk(ctx, da_opus, track); err3 != nil {
				return errors.Wrapf(err3, "Write audio disk")
			}
		} else if codec.MimeType == "audio/PCMU" {
			if da_g711u == nil && cp.SavedAudioFile != "" {
				if da_g711u, err3 = g711writer.New(cp.SavedAudioFile+".g711u", codec.ClockRate, codec.Channels); err3 != nil {
					return errors.Wrapf(err3, "New audio dumper")
				}
				lh.logf("Open g711 writer file=%v, tbn=%v, channels=%v",
					cp.SavedAudioFile+".g711u", codec.ClockRate, codec.Channels)
			}

			if err3 = writeTrackToDisk(ctx, da_g711u, track); err3 != nil {
				return errors.Wrapf(err3, "Write audio disk")
			}
		} else if codec.MimeType == "video/H264" {
			if dv_h264 == nil && cp.SavedVideoFile != "" {
				if dv_h264, err3 = h264writer.New(cp.SavedVideoFile + ".h264"); err3 != nil {
					return errors.Wrapf(err3, "New video dumper")
				}
				lh.logf("Open h264 writer file=%v", cp.SavedVideoFile+".h264")
			}

			if err3 = writeTrackToDisk(ctx, dv_h264, track); err3 != nil {
				lh.logf("Write video disk, error %v", err3)
				return errors.Wrapf(err3, "Write video disk")
			}
		} else {
			lh.logf("Ignore track %v pt=%v", codec.MimeType, codec.PayloadType)
		}
		return nil
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		lh.logln("Got pc.OnTrack")
		var err4 error
		err4 = handleTrack(ctx, track, receiver)
		if err4 != nil {
			codec := track.Codec()
			err4 = errors.Wrapf(err4, "Handle  track %v, pt=%v", codec.MimeType, codec.PayloadType)
			cancel()
		}
	})

	wg.Add(1)
	go func() {
		defer func() {
			// lh.logln("ctx.Done()")
			doClose() // Interrupt the RTCP read.
			wg.Done()
		}()
		<-ctx.Done()
	}()

	wg.Add(1)
	go func() {
		defer func() {
			if aIngester != nil {
				lh.logln("Audio reader exits")
			}
			wg.Done()
		}()

		if aIngester == nil {
			return
		}

		select {
		case <-ctx.Done():
		case <-pcDone.Done():
			lh.logln("PC(ICE+DTLS+SRTP) done, start to read audio packets")
		}

		buf := make([]byte, 1500)
		for ctx.Err() == nil {
			if _, _, err := aIngester.sAudioSender.Read(buf); err != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer func() {
			if aIngester != nil {
				lh.logln("Audio ingester exits")
			}
			wg.Done()
		}()

		if aIngester == nil {
			return
		}

		select {
		case <-ctx.Done():
		case <-pcDone.Done():
			lh.logf("PC(ICE+DTLS+SRTP) done, start to ingest audio %v", sourceAudio)
		}

		for ctx.Err() == nil {
			if err := aIngester.Ingest(ctx); err != nil {
				if errors.Cause(err) == io.EOF {
					// lh.logf("EOF, restart ingest audio %v", sourceAudio)
					continue
				}
				// I don't like this kind of error at all
				if ctx.Err() == nil && strings.Index(err.Error(), "Open") != -1 {
					// lh.logf("Ignore audio err %+v", err)
					continue
				}
				if ctx.Err() == nil {
					lh.logf("Ingest audio err %+v", err)
				}
				break
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer func() {
			if vIngester != nil {
				lh.logln("Video reader exits")
			}
			wg.Done()
		}()

		if vIngester == nil {
			return
		}

		select {
		case <-ctx.Done():
		case <-pcDone.Done():
			lh.logln("PC(ICE+DTLS+SRTP) done, start to read video packets")
		}

		buf := make([]byte, 1500)
		for ctx.Err() == nil {
			if _, _, err := vIngester.sVideoSender.Read(buf); err != nil {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer func() {
			if vIngester != nil {
				lh.logln("Video ingester exits")
			}
			wg.Done()
		}()

		if vIngester == nil {
			return
		}

		select {
		case <-ctx.Done():
		case <-pcDone.Done():
			lh.logf("PC(ICE+DTLS+SRTP) done, start to ingest video %v", sourceVideo)
		}

		for ctx.Err() == nil {
			if err := vIngester.Ingest(ctx); err != nil {
				if errors.Cause(err) == io.EOF {
					// lh.logf("EOF, restart ingest video %v", sourceVideo)
					continue
				}
				// I don't like this kind of error at all
				if ctx.Err() == nil && strings.Index(err.Error(), "Open") != -1 {
					// lh.logf("Ignore video err %+v", err)
					continue
				}
				if ctx.Err() == nil {
					lh.logf("Ingest video err %+v", err)
				}
				break
			}
		}
	}()

	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()

	// 	for {
	// 		select {
	// 		case <-ctx.Done():
	// 			return
	// 			// case <-time.After(5 * time.Second):
	// 			// 	StatRTC.PeerConnection = pc.GetStats()
	// 		}
	// 	}
	// }()

	if cp.Role == "uac" {
		localOffer, err2 = pc.CreateOffer(nil)
		if err2 != nil {
			return errors.Wrapf(err2, "Create Offer")
		}

		if err := pc.SetLocalDescription(localOffer); err != nil {
			return errors.Wrapf(err, "Set local offer %v", localOffer)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-iceCtx.Done():
			// TODO: try more
			newsdp := pc.LocalDescription().SDP
			// only for TURN
			if cp.ICEURLs != "" {
				relayip := getNthItemInALineOfSDP(newsdp, "a=candidate", 5)
				relayport := getNthItemInALineOfSDP(newsdp, "a=candidate", 6)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "c=IN", 3, relayip)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "m=video", 2, relayport)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "m=audio", 2, relayport)
				// newsdp = replaceNthItemInALineOfSDP(newsdp, "o=-", 6, "127.0.0.1")
				// newsdp = removeNthLineOfSDP(newsdp, "a=candidate", 2)
				// newsdp = removeNthLineOfSDP(newsdp, "a=rtcp-rsize", 1)
				// newsdp = newsdp + "a=rtcp:9 IN IP4 0.0.0.0\r\n"
				// newsdp = newsdp + "a=rtcp:" + relayport + " IN IP4 " + relayip + "\r\n"
				// if callKind == "video" {
				// 	newsdp = newsdp + "m=audio 0 RTP/AVP 0\r\na=inactive\r\n"
				// }
			}
			if cp.SdpInCallDetails {
				lh.logf("Offer created\n%s", newsdp)
			} else {
				lh.logln("Offer created")
			}
			sdpToUAChan <- chans.SessionDescription{Type: pc.LocalDescription().Type.String(), SDP: newsdp}
		}

	waitForAnswer:
		for {
			select {
			case <-ctx.Done():
				return nil
			case remoteAnswer = <-sdpFromUAChan:
				// Prefer video
				if strings.Index(remoteAnswer.SDP, "m=video") != -1 {
					audioStart := strings.Index(remoteAnswer.SDP, "m=audio")
					videoStart := strings.Index(remoteAnswer.SDP, "m=video")
					// Remove audio section
					if audioStart != -1 {
						if audioStart < videoStart {
							remoteAnswer.SDP = remoteAnswer.SDP[0:audioStart] + remoteAnswer.SDP[videoStart:]
						} else {
							remoteAnswer.SDP = remoteAnswer.SDP[0:audioStart]
						}
					}
				}
				// Insert a=sendrecv
				remoteAnswer.SDP = remoteAnswer.SDP + "a=sendrecv\r\n"
				// Insert a=mid:0
				remoteAnswer.SDP = remoteAnswer.SDP + "a=mid:0\r\n"
				if cp.SdpInCallDetails {
					lh.logf("Got answer\n%s", remoteAnswer.SDP)
				} else {
					lh.logln("Got answer")
				}
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.NewSDPType(remoteAnswer.Type), SDP: remoteAnswer.SDP}); err != nil {
					return errors.Wrapf(err, "Set remote answer %v", remoteAnswer.SDP)
				}
				if remoteAnswer.Type == "answer" {
					break waitForAnswer
				}
			}
		}
	} else {
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.NewSDPType(remoteOffer.Type), SDP: remoteOffer.SDP}); err != nil {
			return errors.Wrapf(err, "Set remote offer %v", remoteOffer.SDP)
		}

		if callKind == "video" {
			pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendrecv,
			})
		} else {
			pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionSendrecv,
			})
		}

		localAnswer, err2 = pc.CreateAnswer(nil)
		if err2 != nil {
			return errors.Wrapf(err2, "Create Answer")
		}

		if err := pc.SetLocalDescription(localAnswer); err != nil {
			return errors.Wrapf(err, "Set local answer %v", localAnswer)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-iceCtx.Done():
			// TODO: try more
			newsdp := pc.LocalDescription().SDP
			// only for TURN
			if cp.ICEURLs != "" {
				relayip := getNthItemInALineOfSDP(newsdp, "a=candidate", 5)
				relayport := getNthItemInALineOfSDP(newsdp, "a=candidate", 6)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "c=IN", 3, relayip)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "m=video", 2, relayport)
				newsdp = replaceNthItemInALineOfSDP(newsdp, "m=audio", 2, relayport)
				// newsdp = replaceNthItemInALineOfSDP(newsdp, "o=-", 6, "127.0.0.1")
				// newsdp = removeNthLineOfSDP(newsdp, "a=candidate", 2)
				// newsdp = removeNthLineOfSDP(newsdp, "a=rtcp-rsize", 1)
				// newsdp = newsdp + "a=rtcp:9 IN IP4 0.0.0.0\r\n"
				// newsdp = newsdp + "a=rtcp:" + relayport + " IN IP4 " + relayip + "\r\n"
				// if callKind == "video" {
				// 	newsdp = newsdp + "m=audio 0 RTP/AVP 0\r\na=inactive\r\n"
				// }
			}
			if cp.SdpInCallDetails {
				lh.logf("Answer created\n%s", newsdp)
			} else {
				lh.logln("Answer created")
			}
			sdpToUAChan <- chans.SessionDescription{Type: pc.LocalDescription().Type.String(), SDP: newsdp}
		}
	}

	lh.logf("State signaling=%v, ice=%v, conn=%v", pc.SignalingState(), pc.ICEConnectionState(), pc.ConnectionState())

	wg.Wait()
	return nil
}

func writeTrackToDisk(ctx context.Context, w media.Writer, track *webrtc.TrackRemote) error {
	for ctx.Err() == nil {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.Wrapf(err, "Read RTP")
		}

		if w == nil {
			continue
		}

		if err := w.WriteRTP(pkt); err != nil {
			if len(pkt.Payload) <= 2 {
				continue
			}
			// lh.logf("Ignore write RTP %vB err %+v", len(pkt.Payload), err)
		}
	}

	return ctx.Err()
}

func registerDefaultCodecs(m *webrtc.MediaEngine, audioFile string) error {
	// Default Pion Audio Codecs
	if strings.HasSuffix(audioFile, ".g711u") {
		for _, codec := range []webrtc.RTPCodecParameters{
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{webrtc.MimeTypePCMU, 8000, 0, "", nil},
				PayloadType:        0,
			},
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{"telephone-event", 8000, 0, "", nil},
				PayloadType:        101,
			},
		} {
			if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeAudio); err != nil {
				return err
			}
		}
	} else {
		for _, codec := range []webrtc.RTPCodecParameters{
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{webrtc.MimeTypeOpus, 48000, 2, "minptime=10;useinbandfec=1", nil},
				PayloadType:        111,
			},
			{
				RTPCodecCapability: webrtc.RTPCodecCapability{"telephone-event", 8000, 0, "", nil},
				PayloadType:        101,
			},
		} {
			if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeAudio); err != nil {
				return err
			}
		}
	}

	videoRTCPFeedback := []webrtc.RTCPFeedback{{"goog-remb", ""}, {"ccm", "fir"}, {"nack", ""}, {"nack", "pli"}}
	for _, codec := range []webrtc.RTPCodecParameters{
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{webrtc.MimeTypeH264, 90000, 0, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f", videoRTCPFeedback},
			PayloadType:        102,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{"video/rtx", 90000, 0, "apt=102", nil},
			PayloadType:        121,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{webrtc.MimeTypeH264, 90000, 0, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f", videoRTCPFeedback},
			PayloadType:        125,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{"video/rtx", 90000, 0, "apt=125", nil},
			PayloadType:        107,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{webrtc.MimeTypeH264, 90000, 0, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640032", videoRTCPFeedback},
			PayloadType:        123,
		},
		{
			RTPCodecCapability: webrtc.RTPCodecCapability{"video/rtx", 90000, 0, "apt=123", nil},
			PayloadType:        118,
		},

		{
			RTPCodecCapability: webrtc.RTPCodecCapability{"video/ulpfec", 90000, 0, "", nil},
			PayloadType:        116,
		},
	} {
		if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	return nil
}

type logHelper struct {
	logChan chan<- string
}

func (lh *logHelper) logf(format string, v ...interface{}) {
	lh.logChan <- fmt.Sprintf(format, v...)
}

func (lh *logHelper) logln(v ...interface{}) {
	lh.logChan <- fmt.Sprintf("%s", v...)
}
