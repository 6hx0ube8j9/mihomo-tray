package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"mihomo-tray/internal/domain"
)

const yamlHeader = "# Auto-generated config. DO NOT EDIT.\n\n"

type ComposeResult struct {
	YAML      []byte
	TunDevice string
}

func ComposeRuntimeYAML(cfg domain.TrayConfig, sourceYAML []byte) (*ComposeResult, error) {
	var root yaml.Node

	if len(sourceYAML) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(sourceYAML))
		if err := dec.Decode(&root); err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err 
			}
		} else {
			var extra yaml.Node
			if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
				return nil, errors.New("文件格式异常或缩进错误")
			}
		}
	}

	if len(root.Content) == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return nil, errors.New("配置文件整体结构不正确")
	}

	if len(rootMap.Content) > 0 && rootMap.Content[0].Column != 1 {
		return nil, fmt.Errorf("配置文件第一行不能有空格 (第 %d 列多出空格)", rootMap.Content[0].Column)
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
	} else {
		deleteKeys(rootMap, "mixed-port")
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

	putTop("external-controller-cors", buildCORSNode(cfg.Config.ExternalControllerCors.AllowPrivateNetwork, cfg.Config.ExternalControllerCors.AllowOrigins))
	tunDevice, tunTopNodes := patchTunNode(rootMap, cfg.Config.Tun.Enable)
	if len(tunTopNodes) > 0 {
		topNodes = append(topNodes, tunTopNodes...)
	}

	rootMap.Content = append(topNodes, rootMap.Content...)
	clearComments(&root)

	outBytes, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("运行配置序列化失败: %w", err)
	}

	return &ComposeResult{
		YAML:      []byte(yamlHeader + string(outBytes)),
		TunDevice: tunDevice,
	}, nil
}

func buildCORSNode(allowPrivateNetwork *bool, allowOrigins []string) *yaml.Node {
	corsNode := &yaml.Node{Kind: yaml.MappingNode}

	k1 := &yaml.Node{Kind: yaml.ScalarNode, Value: "allow-private-network"}
	var v1 yaml.Node
	allowPrivate := true
	if allowPrivateNetwork != nil {
		allowPrivate = *allowPrivateNetwork
	}
	_ = v1.Encode(allowPrivate)

	k2 := &yaml.Node{Kind: yaml.ScalarNode, Value: "allow-origins"}
	var v2 yaml.Node
	_ = v2.Encode(allowOrigins)

	corsNode.Content = append(corsNode.Content, k1, &v1, k2, &v2)
	return corsNode
}

func patchTunNode(rootMap *yaml.Node, enable bool) (tunDevice string, extraTop []*yaml.Node) {
	tunIdx, tunV := findKey(rootMap, "tun")
	if tunIdx > 0 && tunV.Kind == yaml.MappingNode {
		tunDevice = getString(tunV, "device")
		enableIdx, _ := findKey(tunV, "enable")
		if enableIdx > 0 {
			var newVal yaml.Node
			_ = newVal.Encode(enable)
			tunV.Content[enableIdx] = &newVal
		} else {
			ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
			var ev yaml.Node
			_ = ev.Encode(enable)
			tunV.Content = append([]*yaml.Node{ek, &ev}, tunV.Content...)
		}
		return tunDevice, nil
	}

	if tunIdx > 0 {
		deleteKeys(rootMap, "tun")
	}

	tunK := &yaml.Node{Kind: yaml.ScalarNode, Value: "tun"}
	tunV = &yaml.Node{Kind: yaml.MappingNode}
	ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
	var ev yaml.Node
	_ = ev.Encode(enable)
	tunV.Content = []*yaml.Node{ek, &ev}

	return "", []*yaml.Node{tunK, tunV}
}

// ---------------- AST ----------------

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
