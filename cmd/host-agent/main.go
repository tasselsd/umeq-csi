package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/openxiaoma/umeq-csi/pkg/wrapper"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/pkg/transport"
)

var etcdcli *clientv3.Client

func init() {
	tlsInfo := transport.TLSInfo{
		CertFile:      "etcd.crt",
		KeyFile:       "etcd.key",
		TrustedCAFile: "etcd-ca.crt",
	}
	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		log.Fatal(err)
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"192.168.3.35:2379"},
		DialTimeout: 5 * time.Second,
		TLS:         tlsConfig,
	})
	if err != nil {
		log.Fatal(err)
	}
	etcdcli = cli
}

var diskRoot string = "/fs/trust/vm/csi/"

func main() {
	app := iris.New()

	app.Post("/disk/{name:string}/{size:int64}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		size := ctx.Params().GetInt64Default("size", 1024*1024*10)
		qcowPath := diskRoot + name + ".qcow2"
		cmd := exec.Command("qemu-img", "create", "-f", "qcow2", qcowPath, fmt.Sprintf("%d", size))
		if out, err := cmd.Output(); err != nil {
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			log.Println("create qcow2 err:", err)
			return
		} else {
			log.Println("create qcow2:", string(out))
		}
	})

	app.Put("/disk/{name:string}/{size:int64}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		size, err := ctx.Params().GetInt64("size")
		if err != nil {
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			return
		}
		qcowPath := diskRoot + name + ".qcow2"
		cmd := exec.Command("qemu-img", "resize", qcowPath, fmt.Sprintf("%d", size))
		if out, err := cmd.Output(); err != nil {
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			return
		} else {
			fmt.Println(string(out))
		}
	})

	app.Delete("/disk/{name:string}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		err := os.Remove(diskRoot + name + ".qcow2")
		if err != nil {
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			log.Println("delete qcow2 err:", err)
			return
		}
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		resp, err := etcdcli.Delete(c, "/xiaomakai/"+name)
		if err != nil {
			log.Println("etcd delete ERR:", err)
		} else {
			log.Printf("etcd resp:%v\n", resp)
		}
		fmt.Println("Removed ", name)
	})

	app.Post("/disk/{name:string}/publish/{node:string}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		node := ctx.Params().GetString("node")
		qcow2Path := diskRoot + name + ".qcow2"
		err := wrapper.Exec(node, fmt.Sprintf("drive_add 0 if=none,format=qcow2,file=%s,id=%s", qcow2Path, name))
		if err != nil {
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			log.Println("publish err:", err)
			return
		}

		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r, err := etcdcli.Get(c, "/xiaomakai/"+name)
		if err != nil {
			panic(err)
		}
		if r.Count == 0 {
			id := NextID()
			etcdcli.Put(c, "/xiaomakai/"+name, id)
			r, err = etcdcli.Get(c, "/xiaomakai/"+name)
			if err != nil {
				panic(err)
			}
		}

		err = wrapper.Exec(node, fmt.Sprintf("device_add virtio-blk-pci,drive=%s,id=%s,serial=%s", name, name, r.Kvs[0].Value))
		if err != nil {
			err = wrapper.Exec(node, "drive_del "+name)
			if err != nil {
				log.Panicln("error:", err.Error())
			}
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": err.Error(),
			})
			log.Println("device_add err:", err)
			return
		}
	})

	app.Delete("/disk/{name:string}/publish/{node:string}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		node := ctx.Params().GetString("node")
		err := wrapper.Exec(node, "device_del "+name)
		if err != nil {
			err = wrapper.Exec(node, "drive_del "+name)
			if err != nil {
				ctx.StatusCode(500)
				ctx.JSON(iris.Map{
					"message": err.Error(),
				})
				log.Println("unpushlish err:", err)
				return
			}
		}
	})

	app.Get("/dev-path/{name:string}", func(ctx iris.Context) {
		name := ctx.Params().GetString("name")
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r, err := etcdcli.Get(c, "/xiaomakai/"+name)
		if err != nil {
			panic(err)
		}
		if r.Count == 0 {
			message := fmt.Sprintf("volume %s not found! not published yet?", name)
			ctx.StatusCode(500)
			ctx.JSON(iris.Map{
				"message": message,
			})
			log.Println(message)
			return
		}
		ctx.Write([]byte("/dev/disk/by-id/virtio-"))
		ctx.Write(r.Kvs[0].Value)
	})

	app.Get("/capacity", func(ctx iris.Context) {
		ctx.JSON(iris.Map{
			"Available":         1024 * 1024 * 1024 * 1024 * 2,
			"MaximumVolumeSize": 1024 * 1024 * 1024 * 100,
			"MinimumVolumeSize": 1024 * 1024 * 10,
		})
	})

	app.Listen(":8080")
}

var m sync.Mutex

func NextID() string {
	m.Lock()
	defer m.Unlock()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := etcdcli.Get(c, "/xiaomakai/id")
	if err != nil {
		panic(err)
	}
	if r.Count == 0 {
		etcdcli.Put(c, "/xiaomakai/id", "1")
	} else {
		value, _ := strconv.Atoi(string(r.Kvs[0].Value))
		value += 1
		etcdcli.Put(c, "/xiaomakai/id", fmt.Sprintf("%d", value))
		return string(r.Kvs[0].Value)
	}
	return "0"
}
