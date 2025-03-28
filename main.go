package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Representations map[string]Representation
	Server          Server
	Ingester        Ingester
}

type Representation struct {
	Width     uint32
	Height    uint32
	Pipe      string
	Id        string
	Timescale uint32
}

type Ingester struct {
	SegmentDuration uint32 `json:"segment_duration"`
	Horizon         int    `json:"horizon"`
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

	var wg sync.WaitGroup

	for key, repr := range config.Representations {
		fmt.Printf("Representation #%s on pipe %s\n", key, repr.Pipe)
		repr.Id = key

		namedPipe, err := os.OpenFile(repr.Pipe, syscall.O_RDWR|syscall.O_NONBLOCK, os.ModeNamedPipe)
		if err != nil {
			panic(err)
		}
		defer namedPipe.Close() // will be closed at main() exit as a stack

		stream := &InputStream{
			repr: &repr,
		}

		wg.Add(1)
		streams = append(streams, stream)

		go func() {
			defer wg.Done()
			stream.Serve() // register HTTP handler
			stream.Parse(namedPipe)
		}()

	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sizes_map := make(map[string][]uint32)
		latest_time := streams[0].timestamp

		last_frag := streams[0].lastFrag

		for _, stream := range streams {
			// gli stream potrebbero essere inizializzati in tempi diversi (primo moov atom)
			// si assume però che i dati arrivino in sincronia
			if stream.timestamp.After(latest_time) {
				latest_time = stream.timestamp
			}

			// TODO: qui si incorre in inconsistenza se per una traccia non è ancora arrivato il nuovo segmento e la window non shifta
			sizes_map[stream.repr.Id] = stream.sizesWindow

			// ritorno il frammento con PTS presente su tutti gli stream
			if stream.lastFrag.Pts < last_frag.Pts {
				fmt.Println(" !! PTS drift !! ")
				last_frag = stream.lastFrag
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Timing-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(Manifest{
			Config:   config.Ingester,
			Start:    latest_time,
			Epoch:    uint64(latest_time.UnixMilli()),
			Forecast: sizes_map,
			Last:     last_frag,
		})
	})
	http.ListenAndServe(config.Server.Address, nil)

	wg.Wait()

}
