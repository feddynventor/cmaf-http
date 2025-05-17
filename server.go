package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/justincormack/go-memfd"
	"golang.org/x/sys/unix"
)

type Server struct {
	Address string
}

type Manifest struct {
	Config          Ingester                   `json:"config"`
	Start           time.Time                  `json:"start"`
	Epoch           uint64                     `json:"epoch"`
	Head            uint32                     `json:"head"`
	Representations map[string]*Representation `json:"representations"`
	Keyframes       map[string][]*Fragment     `json:"keyframes"`
}

func (stream *InputStream) Serve() {
	http.HandleFunc("/"+stream.repr.Id+"/", func(w http.ResponseWriter, r *http.Request) {
		index, noIndexProvided := strconv.ParseUint(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 64)

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Timing-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "ruddr-pts, ruddr-segment-length")

		// stream not yet initialized
		if f := stream.GetLastFragment(); f == nil {
			w.WriteHeader(http.StatusNotAcceptable)
			return
		}

		if noIndexProvided != nil {
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			// io.Copy(w, bytes.NewReader(stream.moov))  // TODO: compare
			if _, err := w.Write(stream.moov); err != nil {
				fmt.Println("Error writing moov:", err)
			}
			return
		}

		// if requested is not a keyframe, it's a bad request
		fragment, keyedIndex := stream.GetPlayableFragment(uint32(index))
		if keyedIndex != int(index) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if fragment == nil {
			// IMPR: you can redirect 302 to the correct resource or segment
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "Fragment %d not found", index)
			return
		}

		segment, amount := stream.GetNextFragments(fragment)
		if segment == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "Segment starting from fragment %d not complete yet", index)
			return
		}

		// debugging timestamp only
		// w.Header().Set("ruddr-ingester", stream.timestamp.Add(time.Duration(fragment.Pts*float32(math.Pow10(9)))).Format(time.RFC3339Nano))
		w.Header().Set("Ruddr-Pts", fmt.Sprintf("%.4f", fragment.Pts))    // keyframe presentation time
		w.Header().Set("Ruddr-Segment-Length", fmt.Sprintf("%d", amount)) // length in fragments
		// the next keyframed fragment can be calculated as = current + amount

		fds := make([]*memfd.Memfd, 0)
		segmentSize := int64(0)
		for _, frag := range segment {
			fds = append(fds, frag.fd)
			segmentSize += int64(frag.ByteLength)
		}

		serveFile(w, r, fds, segmentSize)
	})

}

func serveFile(w http.ResponseWriter, r *http.Request, fds []*memfd.Memfd, size int64) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.WriteHeader(http.StatusOK)

	// Ensure headers are flushed before hijacking
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		http.Error(w, "Hijack failed", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Get raw file descriptor of the TCP connection
	tcpFile, err := conn.(*net.TCPConn).File()
	if err != nil {
		fmt.Println("Failed to get TCP file:", err)
		return
	}
	defer tcpFile.Close()
	tcpFd := int(tcpFile.Fd())

	for _, fd := range fds {
		offset := int64(0)
		for offset < size {
			n, err := unix.Sendfile(tcpFd, int(fd.Fd()), &offset, int(size-offset))
			if err != nil {
				fmt.Println("sendfile failed:", err)
				return
			}
			if n == 0 {
				break
			}
		}
	}
}
