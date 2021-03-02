package main

import (
	"io"
	"io/ioutil"
	"os"
	"strings"
)

// Reads all .json files in the current folder
// and encodes them as strings literals in textfiles.go
func main() {
	fs, _ := ioutil.ReadDir("third_party/OpenAPI")
	out, _ := os.Create("proto/swagger.pb.go")
	out.Write([]byte("package milpacs \n\nconst (\n"))
	for _, f := range fs {
		if strings.HasSuffix(f.Name(), ".json") {
			name := strings.TrimPrefix(f.Name(), "milpacs.")
			out.Write([]byte(strings.TrimSuffix(name, ".json") + " = `"))
			f, _ := os.Open("third_party/OpenAPI/" + f.Name())
			io.Copy(out, f)
			out.Write([]byte("`\n"))
		}
	}
	out.Write([]byte(")\n"))
}