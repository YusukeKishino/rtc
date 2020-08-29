package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
	"github.com/go-webpack/webpack"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/sourcegraph/jsonrpc2"
	websocketjsonrpc2 "github.com/sourcegraph/jsonrpc2/websocket"
	ginlogrus "github.com/toorop/gin-logrus"
)

var (
	cert string
	key  string
	addr string
	env  string
)

const (
	Dev  = "development"
	Prod = "production"
)

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	})
}

func parse() bool {
	flag.StringVar(&cert, "cert", "", "cert file")
	flag.StringVar(&key, "key", "", "key file")
	flag.StringVar(&addr, "a", ":8080", "address to use")
	help := flag.Bool("h", false, "help info")
	flag.Parse()

	e := os.Getenv("RTC_ENV")
	if e != "" {
		env = e
	} else {
		env = Dev
	}

	if *help {
		showHelp()
		return false
	}
	return true
}

func showHelp() {
	fmt.Printf("Usage:%s {params}\n", os.Args[0])
	fmt.Println("      -c {config file}")
	fmt.Println("      -cert {cert file}")
	fmt.Println("      -key {key file}")
	fmt.Println("      -a {listen addr}")
	fmt.Println("      -h (show help info)")
}

func main() {
	parse()
	setupWebpack()

	engine := gin.New()
	engine.Use(ginlogrus.Logger(logrus.StandardLogger()), gin.Recovery())
	engine.HTMLRender = loadTemplates("./cmd/server/views")

	if env == Prod {
		engine.GET("/webpack/*name", func(c *gin.Context) {
			c.File("frontend/public/webpack/" + c.Param("name"))
		})
	}
	engine.GET("/favicon.ico", func(c *gin.Context) {
		c.File("config/favicon.png")
	})

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	handler := NewHandler()
	engine.GET("/ws", func(ctx *gin.Context) {
		con, err := upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
		if err != nil {
			ctx.Error(err)
			return
		}
		defer con.Close()

		p := &peerContext{}
		c := context.WithValue(ctx, peerCtxKey, p)
		jc := jsonrpc2.NewConn(c, websocketjsonrpc2.NewObjectStream(con), handler)

		<-jc.DisconnectNotify()

		if p.peer != nil {
			logrus.Infoln("Closing peer")
			p.peer.Close()
		}
	})

	engine.GET("/", func(c *gin.Context) {
		c.HTML(200, "index.html.tmpl", gin.H{})
	})

	if key != "" && cert != "" {
		err := engine.RunTLS(addr, cert, key)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	} else {
		err := engine.Run(addr)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}

func setupWebpack() {
	webpack.DevHost = "localhost:3808" // default
	webpack.Plugin = "manifest"        // defaults to stats for compatability
	webpack.FsPath = "./frontend/public/webpack"
	webpack.Init(env == Dev)
}

func loadTemplates(templatesDir string) multitemplate.Renderer {
	r := multitemplate.NewRenderer()

	layouts, err := filepath.Glob(templatesDir + "/layouts/*.html.tmpl")
	if err != nil {
		panic(err.Error())
	}

	pages, err := filepath.Glob(templatesDir + "/pages/*.html.tmpl")
	if err != nil {
		panic(err.Error())
	}

	funcMap := template.FuncMap{
		"asset": webpack.AssetHelper,
	}

	// Generate our templates map from our layouts/ and pages/ directories
	for _, include := range pages {
		layoutCopy := make([]string, len(layouts))
		copy(layoutCopy, layouts)
		files := append(layoutCopy, include)
		r.AddFromFilesFuncs(filepath.Base(include), funcMap, files...)
	}
	return r
}
