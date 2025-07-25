package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Representations map[string]*Representation
	Server          Server
	Ingester        Ingester
}

type Representation struct {
	Width     uint32 `json:"width"`
	Height    uint32 `json:"height"`
	Log       bool   `json:"-"`
	Pipe      string `json:"-"`
	Id        string `json:"-"`
	Timescale uint32 `json:"-"`
}

type Forecast map[string][]*Fragment // per each presentation - contains Update, or size of the fragment + keyframe flag

type Ingester struct {
	HeapSize            uint32 `json:"-"`
	FragmentDuration    uint32 `json:"fragment_duration"`
	ControllerFrequency int    `json:"controller_frequency"`
	Horizon             int    `json:"horizon"`
}

var streams []*InputStream
var config Config

func main() {

	configFile := "config.toml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		fmt.Printf("Error loading config: %s\n", err)
		os.Exit(1)
	}

	forecast := sync.Map{}
	dataChannel := make(chan []byte)
	broadcaster := NewBroadcaster(dataChannel)

	if err := broadcaster.Start(); err != nil {
		fmt.Printf("Failed to start broadcaster: %v", err)
	}
	defer broadcaster.Stop()
	http.HandleFunc(config.Server.Root+"/events", broadcaster.HandlerFunc())

	var wg sync.WaitGroup

	for streamId, repr := range config.Representations {
		fmt.Printf("Representation #%s on pipe %s\n", streamId, repr.Pipe)
		repr.Id = streamId

		namedPipe, err := os.OpenFile(repr.Pipe, syscall.O_RDWR|syscall.O_NONBLOCK, os.ModeNamedPipe)
		if err != nil {
			panic(err)
		}
		defer namedPipe.Close() // will be closed at main() exit as a stack

		stream := &InputStream{
			repr:            repr,
			fragmentsWindow: NewCircularBuffer[Fragment](config.Ingester.Horizon), // items (windows) are indexed by pts
		}

		wg.Add(1)
		streams = append(streams, stream)

		go func() {
			toSend := 0 // keep track of how many to send still via SSE
			for update := range stream.fragmentsWindow.Updates() {
				window, _ := forecast.LoadOrStore(stream.fragmentsWindow.latest.Pts, &sync.Map{})
				window.(*sync.Map).Store(streamId, update)

				if w, ok := forecast.Load(stream.fragmentsWindow.latest.Pts); ok {
					// IMPR: len for sync.Maps is not available
					regularMap, count := ConvertSyncMapToMap[[]*Fragment](w.(*sync.Map))

					// group all representation per update
					if count < len(config.Representations) {
						continue
					}
					// fmt.Println(stream.sizesWindow.latest.Pts, "Common")
					// lastCommonPts = float32(stream.fragmentsWindow.latest.Pts)
					// lastCommonSeq = uint32(stream.fragmentsWindow.latest.Sequence)

					if data, err := json.Marshal(struct {
						Pts    float32  `json:"pts"`
						Seq    uint32   `json:"seq"`
						Window Forecast `json:"window"`
					}{
						Pts:    stream.fragmentsWindow.latest.Pts,
						Seq:    stream.fragmentsWindow.latest.Sequence,
						Window: regularMap,
					}); err == nil {
						toSend++
						if toSend == config.Ingester.ControllerFrequency {
							dataChannel <- data
							toSend = 0
						}
					}
				}

			}
		}()

		go func() {
			defer wg.Done()
			stream.Serve() // register HTTP handler
			stream.Parse(namedPipe)
		}()

	}

	http.HandleFunc(config.Server.Root+"/", func(w http.ResponseWriter, r *http.Request) {
		common_start_time := streams[0].timestamp
		lastSeqNumber := uint32(streams[0].lastSeqNumber)

		for _, stream := range streams {
			// gli stream _possono_ essere inizializzati in tempi diversi (primo moov atom)
			if stream.timestamp.After(common_start_time) {
				common_start_time = stream.timestamp
			}
			// gli stream _dovrebbero_ avere in sincronia lo stesso numero di sequenza
			if stream.lastSeqNumber < lastSeqNumber {
				lastSeqNumber = stream.lastSeqNumber
				fmt.Println("! SEQ number mismatch ! Gathering lowest !z")
			}
		}
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Expires", "0")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "ruddr-time")
		w.Header().Set("Ruddr-Time", fmt.Sprintf("%d", time.Now().UnixMilli()))
		w.Header().Set("Timing-Allow-Origin", "*")

		keyframes := make(map[string][]*Fragment)
		for _, stream := range streams {
			keyframes[stream.repr.Id] = stream.keyframes
		}

		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(Manifest{
			Config:          config.Ingester,
			Start:           common_start_time,
			Head:            lastSeqNumber,
			Epoch:           uint64(common_start_time.UnixMilli()),
			Representations: config.Representations,
			Keyframes:       keyframes,
		})
	})
	http.ListenAndServe(config.Server.Address, nil)

	wg.Wait()

}

func ConvertSyncMapToMap[T any](syncMap *sync.Map) (map[string]T, int) {
	regularMap := make(map[string]T)

	syncMap.Range(func(key, value interface{}) bool {
		k, ok1 := key.(string)
		v, ok2 := value.(T)
		if ok1 && ok2 {
			regularMap[k] = v
		}
		return true // iterate
	})

	return regularMap, len(regularMap)
}
