package core

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"

	"mihomo-tray/internal/domain"
)

const yamlHeader = "# Auto-generated config. DO NOT EDIT.\n\n"

func clearComments(node *yaml.Node) {
	if node == nil {
		return
	}
	node.HeadComment = ""
	node.LineComment = ""
	node.FootComment = ""
	for _, child := range node.Content {
		clearComments(child)
	}
}

func BuildRuntimeYAML(cfg domain.TrayConfig, sourceAbsPath string) ([]byte, map[string]string, error) {
	var root yaml.Node
	extracted := make(map[string]string)

	if sourceAbsPath != "" {
		content, err := os.ReadFile(sourceAbsPath)
		if err != nil {
			return nil, nil, fmt.Errorf("底稿读取失败: %w", err)
		}
		if err := yaml.Unmarshal(content, &root); err != nil {
			return nil, nil, fmt.Errorf("YAML 格式或结构损坏: %w", err)
		}
	}

	if len(root.Content) == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("YAML 根节点不是 Mapping 类型")
	}

	deleteKeys(rootMap, "redir-port", "tproxy-port")

	var topNodes []*yaml.Node

	putTop := func(key string, val any) {
		k, _ := popKey(rootMap, key) 
		if k == nil {
			k = &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		}
		var newVal yaml.Node
		if err := newVal.Encode(val); err != nil {
			return
		}
		topNodes = append(topNodes, k, &newVal)
	}

	if cfg.Config.Mode != "" { putTop("mode", cfg.Config.Mode) }
	if cfg.Config.LogLevel != "" { putTop("log-level", cfg.Config.LogLevel) }
	if cfg.Config.AllowLan != nil { putTop("allow-lan", *cfg.Config.AllowLan) }
	if cfg.Config.UnifiedDelay != nil { putTop("unified-delay", *cfg.Config.UnifiedDelay) }

	if cfg.Config.MixedPort != nil && *cfg.Config.MixedPort > 0 {
		putTop("mixed-port", *cfg.Config.MixedPort)
		extracted["port"] = strconv.Itoa(*cfg.Config.MixedPort)
	} else {
		deleteKeys(rootMap, "mixed-port")
		extracted["port"] = "0"
	}
	if cfg.Config.Port != nil && *cfg.Config.Port > 0 {
		putTop("port", *cfg.Config.Port)
	} else {
		deleteKeys(rootMap, "port")
	}
	if cfg.Config.SocksPort != nil && *cfg.Config.SocksPort > 0 {
		putTop("socks-port", *cfg.Config.SocksPort)
	} else {
		deleteKeys(rootMap, "socks-port")
	}

	if cfg.Config.ExternalController != "" { putTop("external-controller", cfg.Config.ExternalController) }
	if cfg.Config.ExternalControllerPipe != "" { putTop("external-controller-pipe", cfg.Config.ExternalControllerPipe) }
	if cfg.Config.Secret != nil { putTop("secret", *cfg.Config.Secret) }
	if cfg.Config.ExternalUI != "" { putTop("external-ui", cfg.Config.ExternalUI) }
	
	if cfg.Config.ExternalUIURL != nil && *cfg.Config.ExternalUIURL != "" {
		putTop("external-ui-url", *cfg.Config.ExternalUIURL)
	} else {
		deleteKeys(rootMap, "external-ui-url")
	}
	
	if cfg.Config.ExternalUIName != "" { putTop("external-ui-name", cfg.Config.ExternalUIName) }
	extracted["external-ui-name"] = cfg.Config.ExternalUIName

	corsNode := &yaml.Node{Kind: yaml.MappingNode}
	k1 := &yaml.Node{Kind: yaml.ScalarNode, Value: "allow-private-network"}
	var v1 yaml.Node
	if cfg.Config.ExternalControllerCors.AllowPrivateNetwork != nil {
		_ = v1.Encode(*cfg.Config.ExternalControllerCors.AllowPrivateNetwork)
	} else {
		_ = v1.Encode(true)
	}
	k2 := &yaml.Node{Kind: yaml.ScalarNode, Value: "allow-origins"}
	var v2 yaml.Node
	_ = v2.Encode(cfg.Config.ExternalControllerCors.AllowOrigins)
	corsNode.Content = append(corsNode.Content, k1, &v1, k2, &v2)
	putTop("external-controller-cors", corsNode)	

	tunIdx, tunV := findKey(rootMap, "tun")
	if tunIdx > 0 && tunV.Kind == yaml.MappingNode {
		extracted["tun_device"] = getString(tunV, "device")
		
		enableIdx, _ := findKey(tunV, "enable")
		if enableIdx > 0 {
			var newVal yaml.Node
			_ = newVal.Encode(cfg.Config.Tun.Enable)
			tunV.Content[enableIdx] = &newVal
		} else {
			ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
			var ev yaml.Node
			_ = ev.Encode(cfg.Config.Tun.Enable)
			tunV.Content = append([]*yaml.Node{ek, &ev}, tunV.Content...)
		}
	} else {
		if tunIdx > 0 {
			deleteKeys(rootMap, "tun")
		}
		
		extracted["tun_device"] = ""
		
		tunK := &yaml.Node{Kind: yaml.ScalarNode, Value: "tun"}
		tunV := &yaml.Node{Kind: yaml.MappingNode}
		
		ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
		var ev yaml.Node
		_ = ev.Encode(cfg.Config.Tun.Enable)
		tunV.Content = []*yaml.Node{ek, &ev}
		topNodes = append(topNodes, tunK, tunV)
	}

	rootMap.Content = append(topNodes, rootMap.Content...)
	clearComments(&root)

	outBytes, err := yaml.Marshal(&root)
	if err != nil {
		return nil, nil, fmt.Errorf("运行时配置合成失败: %w", err)
	}

	return append([]byte(yamlHeader), outBytes...), extracted, nil
}

func findKey(node *yaml.Node, key string) (int, *yaml.Node) {
	if node == nil || node.Kind != yaml.MappingNode {
		return -1, nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i + 1, node.Content[i+1]
		}
	}
	return -1, nil
}

func popKey(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	idx, v := findKey(node, key)
	if idx > 0 {
		k := node.Content[idx-1]
		node.Content = append(node.Content[:idx-1], node.Content[idx+1:]...)
		return k, v
	}
	return nil, nil
}

func deleteKeys(node *yaml.Node, keys ...string) {
	for _, key := range keys {
		popKey(node, key)
	}
}

func getString(node *yaml.Node, key string) string {
	_, v := findKey(node, key)
	if v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
