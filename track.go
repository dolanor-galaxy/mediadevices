package mediadevices

import (
	"fmt"
	"io"
	"math/rand"
	"sync/atomic"

	"github.com/pion/mediadevices/pkg/codec"
	"github.com/pion/mediadevices/pkg/driver"
	mio "github.com/pion/mediadevices/pkg/io"
	"github.com/pion/webrtc/v2"
	"github.com/pion/webrtc/v2/pkg/media"
)

// Tracker is an interface that represent MediaStreamTrack
// Reference: https://w3c.github.io/mediacapture-main/#mediastreamtrack
type Tracker interface {
	Track() *webrtc.Track
	LocalTrack() LocalTrack
	Stop()
	OnEnded(func(error))
}

type LocalTrack interface {
	WriteSample(s media.Sample) error
	Codec() *webrtc.RTPCodec
	ID() string
	Kind() webrtc.RTPCodecType
}

type track struct {
	t LocalTrack
	s *sampler

	onErrorHandler atomic.Value // func(error)
}

func newTrack(codecs []*webrtc.RTPCodec, trackGenerator TrackGenerator, d driver.Driver, codecName string) (*track, error) {
	var selectedCodec *webrtc.RTPCodec
	for _, c := range codecs {
		if c.Name == codecName {
			selectedCodec = c
			break
		}
	}
	if selectedCodec == nil {
		return nil, fmt.Errorf("track: %s is not registered in media engine", codecName)
	}

	t, err := trackGenerator(
		selectedCodec.PayloadType,
		rand.Uint32(),
		d.ID(),
		selectedCodec.Type.String(),
		selectedCodec,
	)
	if err != nil {
		return nil, err
	}

	return &track{
		t: t,
		s: newSampler(t),
	}, nil
}

func (t *track) OnEnded(handler func(error)) {
	t.onErrorHandler.Store(handler)
}

func (t *track) onError(err error) {
	handler := t.onErrorHandler.Load()
	if handler != nil {
		handler.(func(error))(err)
	}
}

func (t *track) Track() *webrtc.Track {
	return t.t.(*webrtc.Track)
}

func (t *track) LocalTrack() LocalTrack {
	return t.t
}

type videoTrack struct {
	*track
	d           driver.Driver
	constraints MediaTrackConstraints
	encoder     io.ReadCloser
}

var _ Tracker = &videoTrack{}

func newVideoTrack(opts *MediaDevicesOptions, d driver.Driver, constraints MediaTrackConstraints) (*videoTrack, error) {
	codecName := constraints.CodecName
	t, err := newTrack(opts.codecs[webrtc.RTPCodecTypeVideo], opts.trackGenerator, d, codecName)
	if err != nil {
		return nil, err
	}

	err = d.Open()
	if err != nil {
		return nil, err
	}

	vr := d.(driver.VideoRecorder)
	r, err := vr.VideoRecord(constraints.Media)
	if err != nil {
		return nil, err
	}

	if constraints.VideoTransform != nil {
		r = constraints.VideoTransform(r)
	}

	encoder, err := codec.BuildVideoEncoder(r, constraints.Media)
	if err != nil {
		return nil, err
	}

	vt := videoTrack{
		track:       t,
		d:           d,
		constraints: constraints,
		encoder:     encoder,
	}

	go vt.start()
	return &vt, nil
}

func (vt *videoTrack) start() {
	var n int
	var err error
	buff := make([]byte, 1024)
	for {
		n, err = vt.encoder.Read(buff)
		if err != nil {
			if e, ok := err.(*mio.InsufficientBufferError); ok {
				buff = make([]byte, 2*e.RequiredSize)
				continue
			}

			vt.track.onError(err)
			return
		}

		if err := vt.s.sample(buff[:n]); err != nil {
			vt.track.onError(err)
			return
		}
	}
}

func (vt *videoTrack) Stop() {
	vt.d.Close()
	vt.encoder.Close()
}

type audioTrack struct {
	*track
	d           driver.Driver
	constraints MediaTrackConstraints
	encoder     io.ReadCloser
}

var _ Tracker = &audioTrack{}

func newAudioTrack(opts *MediaDevicesOptions, d driver.Driver, constraints MediaTrackConstraints) (*audioTrack, error) {
	codecName := constraints.CodecName
	t, err := newTrack(opts.codecs[webrtc.RTPCodecTypeAudio], opts.trackGenerator, d, codecName)
	if err != nil {
		return nil, err
	}

	err = d.Open()
	if err != nil {
		return nil, err
	}

	ar := d.(driver.AudioRecorder)
	reader, err := ar.AudioRecord(constraints.Media)
	if err != nil {
		return nil, err
	}

	if constraints.AudioTransform != nil {
		reader = constraints.AudioTransform(reader)
	}

	encoder, err := codec.BuildAudioEncoder(reader, constraints.Media)
	if err != nil {
		return nil, err
	}

	at := audioTrack{
		track:       t,
		d:           d,
		constraints: constraints,
		encoder:     encoder,
	}
	go at.start()
	return &at, nil
}

func (t *audioTrack) start() {
	buff := make([]byte, 1024)
	sampleSize := uint32(float64(t.constraints.SampleRate) * t.constraints.Latency.Seconds())
	for {
		n, err := t.encoder.Read(buff)
		if err != nil {
			t.track.onError(err)
			return
		}

		if err := t.t.WriteSample(media.Sample{
			Data:    buff[:n],
			Samples: sampleSize,
		}); err != nil {
			t.track.onError(err)
			return
		}
	}
}

func (t *audioTrack) Stop() {
	t.d.Close()
	t.encoder.Close()
}
