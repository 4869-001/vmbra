package clash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Source 是一个订阅来源：一条订阅链接导入的节点集合。
type Source struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	URL           string    `json:"url,omitempty"`
	IntervalHours int       `json:"intervalHours,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Nodes         []Node    `json:"nodes"`
}

// StoreDoc 是 ~/.vmbra/proxy/sources.json 的持久化结构。
type StoreDoc struct {
	Version  int      `json:"version"`
	Selected string   `json:"selected"`
	Sources  []Source `json:"sources"`
}

const storeVersion = 1

// LoadStore 读取订阅库；文件不存在时返回空库（不是错误）。
func LoadStore(dataDir string) (*StoreDoc, error) {
	p := storePath(dataDir)
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &StoreDoc{Version: storeVersion}, nil
		}
		return nil, err
	}
	var doc StoreDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Version == 0 {
		doc.Version = storeVersion
	}
	return &doc, nil
}

// Save 原子写入订阅库。
func (d *StoreDoc) Save(dataDir string) error {
	if d.Version == 0 {
		d.Version = storeVersion
	}
	p := storePath(dataDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Node 按 ID 查找节点。
func (d *StoreDoc) Node(id string) (Node, bool) {
	for _, src := range d.Sources {
		for _, n := range src.Nodes {
			if n.ID == id {
				return n, true
			}
		}
	}
	return Node{}, false
}

// AllNodes 汇总所有来源的节点，按来源顺序展开。
func (d *StoreDoc) AllNodes() []Node {
	out := make([]Node, 0, 32)
	for _, src := range d.Sources {
		out = append(out, src.Nodes...)
	}
	return out
}

// UpsertSource 覆盖同名来源（同名视作同一订阅的刷新）。
func (d *StoreDoc) UpsertSource(src Source) {
	for i := range d.Sources {
		if d.Sources[i].ID == src.ID {
			d.Sources[i] = src
			return
		}
	}
	d.Sources = append(d.Sources, src)
}

// RemoveNode 从所属来源中删除节点。
func (d *StoreDoc) RemoveNode(id string) bool {
	for i := range d.Sources {
		nodes := d.Sources[i].Nodes[:0]
		removed := false
		for _, n := range d.Sources[i].Nodes {
			if n.ID == id {
				removed = true
				continue
			}
			nodes = append(nodes, n)
		}
		d.Sources[i].Nodes = nodes
		if removed {
			if d.Selected == id {
				d.Selected = ""
			}
			return true
		}
	}
	return false
}

// RemoveSource 删除整个来源，并清理选中项。
func (d *StoreDoc) RemoveSource(id string) bool {
	for i := range d.Sources {
		if d.Sources[i].ID != id {
			continue
		}
		for _, n := range d.Sources[i].Nodes {
			if d.Selected == n.ID {
				d.Selected = ""
			}
		}
		d.Sources = append(d.Sources[:i], d.Sources[i+1:]...)
		return true
	}
	return false
}

func storePath(dataDir string) string {
	return filepath.Join(dataDir, "proxy", "sources.json")
}
