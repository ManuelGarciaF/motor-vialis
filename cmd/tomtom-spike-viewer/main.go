// Command tomtom-spike-viewer serves the local files exported by tomtom-spike.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	address := flag.String("address", ":8090", "HTTP listen address")
	directory := flag.String("dir", ".local/tomtom-caba-viewer", "viewer directory")
	flag.Parse()

	index := filepath.Join(*directory, "index.html")
	if _, err := os.Stat(index); err != nil {
		log.Fatalf("viewer not found at %s: %v", index, err)
	}

	files := http.FileServer(http.Dir(*directory))
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(writer, request)
	})
	fmt.Printf("TomTom spike viewer: http://localhost%s\n", *address)
	log.Fatal(http.ListenAndServe(*address, handler))
}
