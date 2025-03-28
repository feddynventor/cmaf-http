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
	Config   Ingester            `json:"config"`
	Start    time.Time           `json:"start"`
	Epoch    uint64              `json:"epoch"`
	Last     *Fragment           `json:"last"`
	Forecast map[string][]uint32 `json:"representations"`
}

func (stream *InputStream) Serve() {
	http.HandleFunc("/"+stream.repr.Id+"/", func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.ParseUint(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], 10, 32)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if err != nil {
			w.WriteHeader(http.StatusOK)
			// io.Copy(w, bytes.NewReader(stream.moov))  // TODO: compare
			if _, err := w.Write(stream.moov); err != nil {
				fmt.Println("Error writing moov:", err)
			}
			return
		}

		frag, ok := stream.fragments.Load(uint32(index))
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "Fragment %d not found", index)
			return
		}

		serveFile(w, r, frag.(*Fragment).fd, int64(frag.(*Fragment).ByteLength))
	})

}

func serveFile(w http.ResponseWriter, r *http.Request, fd *memfd.Memfd, size int64) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Timing-Allow-Origin", "*")
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
