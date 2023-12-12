package main

import (
	"flag"
	"fmt"
	"os"

	sgen "github.com/doeasycode/protoc-gen-swagger/gen"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/gen"
	"github.com/go-kratos/kratos/tool/protobuf/pkg/generator"
)

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println(generator.Version)
		os.Exit(0)
	}

	g := sgen.NewSwaggerGenerator()
	gen.Main(g)
}
