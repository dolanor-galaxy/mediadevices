package mediadevices

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/pion/webrtc/v2"
	"github.com/pion/webrtc/v2/pkg/media"

	"github.com/pion/mediadevices/pkg/codec"
	_ "github.com/pion/mediadevices/pkg/driver/audiotest"
	_ "github.com/pion/mediadevices/pkg/driver/videotest"
	"github.com/pion/mediadevices/pkg/io/audio"
	"github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"
)

func TestGetUserMedia(t *testing.T) {
	codec.Register("MockVideo", codec.VideoEncoderBuilder(func(r video.Reader, p prop.Media) (io.ReadCloser, error) {
		if p.BitRate == 0 {
			return nil, errors.New("wrong codec parameter")
		}
		return &mockVideoCodec{
			r:      r,
			closed: make(chan struct{}),
		}, nil
	}))
	codec.Register("MockAudio", codec.AudioEncoderBuilder(func(r audio.Reader, p prop.Media) (io.ReadCloser, error) {
		return &mockAudioCodec{
			r:      r,
			closed: make(chan struct{}),
		}, nil
	}))

	md := NewMediaDevicesFromCodecs(
		map[webrtc.RTPCodecType][]*webrtc.RTPCodec{
			webrtc.RTPCodecTypeVideo: []*webrtc.RTPCodec{
				&webrtc.RTPCodec{Type: webrtc.RTPCodecTypeVideo, Name: "MockVideo", PayloadType: 1},
			},
			webrtc.RTPCodecTypeAudio: []*webrtc.RTPCodec{
				&webrtc.RTPCodec{Type: webrtc.RTPCodecTypeAudio, Name: "MockAudio", PayloadType: 2},
			},
		},
		WithTrackGenerator(
			func(_ uint8, _ uint32, id, _ string, codec *webrtc.RTPCodec) (
				LocalTrack, error,
			) {
				return newMockTrack(codec, id), nil
			},
		),
	)
	constraints := MediaStreamConstraints{
		Video: func(c *MediaTrackConstraints) {
			c.CodecName = "MockVideo"
			c.Enabled = true
			c.Width = 640
			c.Height = 480
			c.BitRate = 100000
		},
		Audio: func(c *MediaTrackConstraints) {
			c.CodecName = "MockAudio"
			c.Enabled = true
			c.BitRate = 32000
		},
	}
	constraintsWrong := MediaStreamConstraints{
		Video: func(c *MediaTrackConstraints) {
			c.CodecName = "MockVideo"
			c.Enabled = true
			c.Width = 640
			c.Height = 480
			c.BitRate = 0
		},
		Audio: func(c *MediaTrackConstraints) {
			c.CodecName = "MockAudio"
			c.Enabled = true
			c.BitRate = 32000
		},
	}

	// GetUserMedia with broken parameters
	ms, err := md.GetUserMedia(constraintsWrong)
	if err == nil {
		t.Fatal("Expected error, but got nil")
	}

	// GetUserMedia with correct parameters
	ms, err = md.GetUserMedia(constraints)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	tracks := ms.GetTracks()
	if l := len(tracks); l != 2 {
		t.Fatalf("Number of the tracks is expected to be 1, got %d", l)
	}
	tracks[0].OnEnded(func(err error) {
		if err != io.EOF {
			t.Errorf("OnEnded called: %v", err)
		}
	})
	time.Sleep(50 * time.Millisecond)

	for _, t := range tracks {
		t.Stop()
	}

	// Stop and retry GetUserMedia
	ms, err = md.GetUserMedia(constraints)
	if err != nil {
		t.Fatalf("Failed to GetUserMedia after the previsous tracks stopped: %v", err)
	}
	tracks = ms.GetTracks()
	if l := len(tracks); l != 2 {
		t.Fatalf("Number of the tracks is expected to be 1, got %d", l)
	}
	tracks[0].OnEnded(func(err error) {
		if err != io.EOF {
			t.Errorf("OnEnded called: %v", err)
		}
	})
	time.Sleep(50 * time.Millisecond)
}

type mockTrack struct {
	codec *webrtc.RTPCodec
	id    string
}

func newMockTrack(codec *webrtc.RTPCodec, id string) *mockTrack {
	return &mockTrack{
		codec: codec,
		id:    id,
	}
}

func (t *mockTrack) WriteSample(s media.Sample) error {
	return nil
}

func (t *mockTrack) Codec() *webrtc.RTPCodec {
	return t.codec
}

func (t *mockTrack) ID() string {
	return t.id
}

func (t *mockTrack) Kind() webrtc.RTPCodecType {
	return t.codec.Type
}

type mockVideoCodec struct {
	r      video.Reader
	closed chan struct{}
}

func (m *mockVideoCodec) Read(b []byte) (int, error) {
	if _, err := m.r.Read(); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (m *mockVideoCodec) Close() error { return nil }

type mockAudioCodec struct {
	r      audio.Reader
	closed chan struct{}
}

func (m *mockAudioCodec) Read(b []byte) (int, error) {
	buf := make([][2]float32, 100)
	if _, err := m.r.Read(buf); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (m *mockAudioCodec) Close() error { return nil }
