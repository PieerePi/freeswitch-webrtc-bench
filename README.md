# freeswitch-webrtc-bench

WebRTC benchmark for [FreeSWITCH](https://github.com/signalwire/freeswitch).

## About

Freeswitch-webrtc-bench is a loading test tool like [sipp](http://sipp.sourceforge.net/) and [winsip](http://www.touchstone-inc.com/winsip.php), but mainly focus on SIP signalling over websocket and media via WebRTC. It is built on [go-sip-ua](https://github.com/PieerePi/gosip/tree/rtc_bench)/[gosip](https://github.com/PieerePi/go-sip-ua/tree/rtc_bench)/[pion/webrtc](https://github.com/pion/webrtc) and is pure Go.

There is a web client [rztrtcsdk](https://github.com/PieerePi/rztrtcsdk/blob/master/sdk/sdkdemo.html) created by me that you can use to test WebRTC with freeswitch-webrtc-bench.

## Usage

- Compile

  `go build .`

- Create a call profile

  There are some example call profiles in the examples directory that you can modify with reference to the [Call Profile](#call-profile) description.

- Run the test

  Run the UAS first,

  `./freeswitch-webrtc-bench -p examples/uas.json`

  Then in another console, run the UAC,

  `./freeswitch-webrtc-bench -p examples/uac.json`

  After the test is done, there will be some log files generated.

## Call Profile

Here is the call profile structure defined in Go. You can find some example call profiles in the examples directory.

```Go
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
```

## Notes

There is an issue [Change findMatchingCipherSuite](https://github.com/pion/dtls/pull/350) when pion/dtls and FreeSWITCH interoperate. I've modified the code in the vendor directory, but if you run `go mod vendor` command, you might need to modify it again manually.

```patch
diff --git a/vendor/github.com/pion/dtls/v2/util.go b/vendor/github.com/pion/dtls/v2/util.go
index 745182d..cd7249d 100644
--- a/vendor/github.com/pion/dtls/v2/util.go
+++ b/vendor/github.com/pion/dtls/v2/util.go
@@ -11,9 +11,9 @@ func findMatchingSRTPProfile(a, b []SRTPProtectionProfile) (SRTPProtectionProfil
        return 0, false
 }

-func findMatchingCipherSuite(a, b []CipherSuite) (CipherSuite, bool) { //nolint
-       for _, aSuite := range a {
-               for _, bSuite := range b {
+func findMatchingCipherSuite(remote, local []CipherSuite) (CipherSuite, bool) { //nolint
+       for _, aSuite := range local {
+               for _, bSuite := range remote {
                        if aSuite.ID() == bSuite.ID() {
                                return aSuite, true
                        }
```

## Limits

- Only supports one media
  - Since FreeSWITCH doesn't support bundled media, and pion/webrtc doesn't support unbundled media, so there is only one media in a call, either audio or video, you can specify it by setting the mediatype parameter in the call profile.

- Audio call is not fully supported
  - Audio call can be established now; but if the remote party rejects us with 603 Decline, FreeSWITCH won't hang up us.
  - The remote party cannot hear us, because FreeSWITCH doesn't pass our media to the remote party; but we can hear the remote party.

- Call without TURN enabled is not supported
  - UAS ICE state failed if TURN is not enabled. That might be pion's problem (TODO).

## Acknowledgement

- [cloudwebrtc/go-sip-ua](https://github.com/cloudwebrtc/go-sip-ua) SIP UA
- [ghettovoice/gosip](https://github.com/ghettovoice/gosip) SIP Stack
- [pion/webrtc](https://github.com/pion/webrtc) Pion WebRTC
- [ossrs/srs-bench](https://github.com/ossrs/srs-bench/tree/feature/rtc) SRS WebRTC Benchmark
