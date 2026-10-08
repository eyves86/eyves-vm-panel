// loadseed 压测夹具注入器（仅测试机临时使用，不入仓）。
// 生成 N 个被控节点（各带确定 token）与 S 个子用户，并按 -conts-per-node 给每个节点
// 挂容器。容器字段与 agent 心跳载荷逐字段对齐，使稳态心跳不触发结构性变更（不落库），
// 这样量到的就是「纯入站 + 节流」的真实成本。
package main

import (
	"flag"
	"fmt"

	"eyvescloud/internal/config"
)

func main() {
	nodes := flag.Int("nodes", 30000, "被控节点数")
	perNode := flag.Int("conts-per-node", 10, "每节点容器数（0=不挂容器）")
	subusers := flag.Int("subusers", 1000, "子用户数")
	port := flag.Int("port", 18996, "面板端口（写入配置）")
	pass := flag.String("admin-pass", "", "管理员口令（设置则重置管理员密码）")
	flag.Parse()

	if _, err := config.InitConfig(); err != nil {
		panic(err)
	}

	ns := make([]config.Node, *nodes)
	for i := range ns {
		id := fmt.Sprintf("node-%06d", i)
		ns[i] = config.Node{
			ID: id, Name: id, Token: "tok-" + id, Status: "online",
			Version: "2.2.50", CPUCount: 8, RAMTotalMB: 16384, DiskTotalGB: 200,
			ContainerCount: *perNode,
		}
	}

	total := *nodes * *perNode
	cs := make([]config.Container, 0, total)
	cid := 0
	for i := range ns {
		for j := 0; j < *perNode; j++ {
			cid++
			cs = append(cs, config.Container{
				ID: cid, UUID: fmt.Sprintf("u-%06d-%03d", i, j),
				Name: fmt.Sprintf("ct-%06d-%03d", i, j),
				NodeID: ns[i].ID, NodeLocalID: j + 1,
				Status: "running", Virtualization: "lxc", Template: "ubuntu-22.04",
				VCPU: 1, RAMMB: 128, DiskGB: 1,
			})
		}
	}

	subs := make([]config.SubUser, *subusers)
	for i := range subs {
		subs[i].ID = fmt.Sprintf("su-%06d", i)
		subs[i].Username = fmt.Sprintf("user%06d", i)
	}

	config.AppConfig = &config.EyvescloudConfig{
		AdminUser: "admin", Port: *port, SetupComplete: true,
		Containers: cs, NextContainerID: cid + 1,
		SubUsers: subs, Nodes: ns,
		NATPortStart: 20000, NATPortEnd: 65535,
	}
	if err := config.SaveConfig(); err != nil {
		panic(err)
	}
	if *pass != "" {
		if err := config.ResetAdminPassword(*pass); err != nil {
			panic(err)
		}
	}
	fmt.Printf("SEEDED nodes=%d conts=%d subusers=%d\n", *nodes, len(cs), *subusers)
}
