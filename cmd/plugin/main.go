package main

import (
	"os"

	"github.com/tasselsd/umeq-csi/internel/umeq"
)

func main() {
	endpoint := os.Getenv("CSI_ENDPOINT")
	nodeId := os.Getenv("NODE_NAME")
	if endpoint == "" {
		panic("system environment CSI_ENDPOINT must not empty!")
	}
	if nodeId == "" {
		panic("system environment NODE_NAME must not empty!")
	}
	s := umeq.NewNonBlockingGRPCServer()
	csi := umeq.Csi{
		NodeID:        nodeId,
		DriverName:    "umeq-csi.xiaomakai.com",
		VendorVersion: "1.0.0",
	}
	s.Start("unix://"+endpoint, &csi, &csi, &csi)
	s.Wait()
}
