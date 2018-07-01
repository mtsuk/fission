package main

import (
	"fmt"
	"github.com/urfave/cli"
)

func metricsTotalReqCount(c *cli.Context) error {
	client := getClient(c.GlobalString("server"))

	url := c.String("url")
	method := c.String("method")
	window := c.String("window")

	result, err := client.TotalRequestToUrlGet(url, method, window)
	checkErr(err, "get metricsTotalReqCount")

	fmt.Println(result)
	return err
}

func metricsTotalErrorCount(c *cli.Context) error {
	client := getClient(c.GlobalString("server"))

	fn := c.String("function")
	ns := c.String("namespace")
	window := c.String("window")

	result, err := client.TotalErrorRequestToFuncGet(fn, ns, window)
	checkErr(err, "get metricsTotalReqCount")

	fmt.Println(result)
	return err
}
