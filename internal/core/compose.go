package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"mihomo-tray/internal/domain"
)

const yamlHeader = "# Auto-generated config. DO NOT EDIT.\n\n"

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type ComposeResult struct {
	YAML      []byte
	TunDevice string
}

type ConfigError struct {
	Msg string
}

func (e *ConfigError) Error() string { return e.Msg }

func cleanYAMLErr(err error) error {
	if err == nil {
		return nil
	}
	return &ConfigError{Msg: strings.TrimPrefix(err.Error(), "yaml: ")}
}

func parseSingleYAMLMapping(sourceYAML []byte) (*yaml.Node, *yaml.Node, error) {
	trimmed := bytes.TrimPrefix(sourceYAML, utf8BOM)
	if len(bytes.TrimSpace(trimmed)) == 0 {
		return nil, nil, nil
	}

	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, nil
		}
		return nil, nil, cleanYAMLErr(err)
	}

	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, nil, &ConfigError{Msg: "配置文件包含了多份独立配置 ('---')"}
	} else if !errors.Is(err, io.EOF) {
		return nil, nil, cleanYAMLErr(err)
	}

	if len(root.Content) == 0 {
		return nil, nil, nil
	}

	rootMap := root.Content[0]
	if rootMap.Kind != yaml.MappingNode {
		return nil, nil, &ConfigError{Msg: "配置文件缺少核心字段"}
	}

	if len(rootMap.Content) > 0 && rootMap.Content[0].Column != 1 {
		return nil, nil, &ConfigError{Msg: fmt.Sprintf("第 %d 列缩进格式错误", rootMap.Content[0].Column)}
	}

	return &root, rootMap, nil
}

func ValidateProfileStatic(sourceYAML []byte) error {
	_, rootMap, err := parseSingleYAMLMapping(sourceYAML)
	if err != nil {
		return err
	}
	if rootMap == nil || len(rootMap.Content) == 0 {
		return &ConfigError{Msg: "配置文件内容为空"}
	}

	pIdx, _ := findKey(rootMap, "proxies")
	ppIdx, _ := findKey(rootMap, "proxy-providers")
	if pIdx < 0 && ppIdx < 0 {
		return &ConfigError{Msg: "配置文件缺少 proxies 或 proxy-providers 字段"}
	}

	return nil
}

func ComposeRuntimeYAML(cfg domain.TrayConfig, sourceYAML []byte) (*ComposeResult, error) {
	root, rootMap, err := parseSingleYAMLMapping(sourceYAML)
	if err != nil {
		return nil, err
	}

	if root == nil || rootMap == nil {
		rootMap = &yaml.Node{Kind: yaml.MappingNode}
		root = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{rootMap}}
	}

	deleteKeys(rootMap, "redir-port", "tproxy-port")

	var topNodes []*yaml.Node

	putTop := func(key string, val any) {
		deleteKeys(rootMap, key)
		k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		var newVal yaml.Node
		if err := newVal.Encode(val); err != nil {
			return
		}
		topNodes = append(topNodes, k, &newVal)
	}

	if cfg.Config.Mode != "" {
		putTop("mode", cfg.Config.Mode)
	}
	if cfg.Config.LogLevel != "" {
		putTop("log-level", cfg.Config.LogLevel)
	}
	if cfg.Config.AllowLan != nil {
		putTop("allow-lan", *cfg.Config.AllowLan)
	}
	if cfg.Config.UnifiedDelay != nil {
		putTop("unified-delay", *cfg.Config.UnifiedDelay)
	}

	if cfg.Config.GeoAutoUpdate != nil {
		putTop("geo-auto-update", *cfg.Config.GeoAutoUpdate)
	}
	if cfg.Config.GeoUpdateInterval != nil && *cfg.Config.GeoUpdateInterval > 0 {
		putTop("geo-update-interval", *cfg.Config.GeoUpdateInterval)
	}
	if geoxNode := buildGeoXURLNode(cfg.Config.GeoXURL); geoxNode != nil {
		putTop("geox-url", geoxNode)
	}

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

	if cfg.Config.ExternalController != "" {
		putTop("external-controller", cfg.Config.ExternalController)
	}
	if cfg.Config.ExternalControllerPipe != "" {
		putTop("external-controller-pipe", cfg.Config.ExternalControllerPipe)
	}
	if cfg.Config.Secret != nil {
		putTop("secret", *cfg.Config.Secret)
	}
	if cfg.Config.ExternalUI != "" {
		putTop("external-ui", cfg.Config.ExternalUI)
	}

	if cfg.Config.ExternalUIURL != nil && *cfg.Config.ExternalUIURL != "" {
		putTop("external-ui-url", *cfg.Config.ExternalUIURL)
	} else {
		deleteKeys(rootMap, "external-ui-url")
	}

	if cfg.Config.ExternalUIName != "" {
		putTop("external-ui-name", cfg.Config.ExternalUIName)
	}

	putTop("external-controller-cors", buildCORSNode(cfg.Config.ExternalControllerCors.AllowPrivateNetwork, cfg.Config.ExternalControllerCors.AllowOrigins))
	tunDevice, tunTopNodes := patchTunNode(rootMap, cfg.Config.Tun.Enable)
	if len(tunTopNodes) > 0 {
		topNodes = append(topNodes, tunTopNodes...)
	}

	rootMap.Content = append(topNodes, rootMap.Content...)
	clearComments(root)

	outBytes, err := yaml.Marshal(root)
	if err != nil {
		return nil, err
	}

	finalYAML := append([]byte(yamlHeader), outBytes...)

	return &ComposeResult{
		YAML:      finalYAML,
		TunDevice: tunDevice,
	}, nil
}

func buildGeoXURLNode(geox domain.GeoXURLConfig) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	appendPair := func(key, val string) {
		if val == "" {
			return
		}
		k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
		var v yaml.Node
		_ = v.Encode(val)
		node.Content = append(node.Content, k, &v)
	}

	appendPair("mmdb", geox.MMDB)
	appendPair("asn", geox.ASN)
	appendPair("geoip", geox.GeoIP)
	appendPair("geosite", geox.GeoSite)

	if len(node.Content) == 0 {
		return nil
	}
	return node
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
	lastIdx := -1
	for i := 0; i < len(rootMap.Content); i += 2 {
		if rootMap.Content[i].Value == "tun" {
			lastIdx = i
		}
	}

	if lastIdx >= 0 && rootMap.Content[lastIdx+1].Kind == yaml.MappingNode {
		tunV := rootMap.Content[lastIdx+1]
		out := rootMap.Content[:0]
		for i := 0; i < len(rootMap.Content); i += 2 {
			if rootMap.Content[i].Value == "tun" && i != lastIdx {
				continue
			}
			out = append(out, rootMap.Content[i], rootMap.Content[i+1])
		}
		clear(rootMap.Content[len(out):])
		rootMap.Content = out

		tunDevice = strings.TrimSpace(getString(tunV, "device"))
		if tunDevice == "" {
			tunDevice = domain.DefaultTunDevice
		}

		deleteKeys(tunV, "enable")
		ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
		var ev yaml.Node
		_ = ev.Encode(enable)
		tunV.Content = append([]*yaml.Node{ek, &ev}, tunV.Content...)
		return tunDevice, nil
	}

	deleteKeys(rootMap, "tun")

	tunK := &yaml.Node{Kind: yaml.ScalarNode, Value: "tun"}
	tunV := &yaml.Node{Kind: yaml.MappingNode}
	ek := &yaml.Node{Kind: yaml.ScalarNode, Value: "enable"}
	var ev yaml.Node
	_ = ev.Encode(enable)
	tunV.Content = []*yaml.Node{ek, &ev}

	return domain.DefaultTunDevice, []*yaml.Node{tunK, tunV}
}

func deleteKeys(node *yaml.Node, keys ...string) {
	if node == nil || node.Kind != yaml.MappingNode || len(keys) == 0 {
		return
	}
	out := node.Content[:0]
	for i := 0; i < len(node.Content); i += 2 {
		if slices.Contains(keys, node.Content[i].Value) {
			continue
		}
		out = append(out, node.Content[i], node.Content[i+1])
	}
	clear(node.Content[len(out):])
	node.Content = out
}

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

func getString(node *yaml.Node, key string) string {
	_, v := findKey(node, key)
	if v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
