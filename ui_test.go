package graph

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestUIRoundTripDropsUnknownAndKeepsTransparentFalse(t *testing.T) {
	raw := []byte(`{
		"EdgeType": "smoothstep",
		"Breakpoints": ["n1"],
		"StickyNotes": [{
			"ID": "ann_1",
			"X": 0,
			"Y": -40.5,
			"Width": 80,
			"Height": 60,
			"Title": "说明",
			"Transparent": false
		}]
	}`)
	ui, err := ParseUI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ui.StickyNotes) != 1 || ui.StickyNotes[0].Transparent == nil || *ui.StickyNotes[0].Transparent {
		t.Fatalf("parsed %+v", ui)
	}
	out, err := MarshalUI(ui)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("EdgeType")) || bytes.Contains(out, []byte("Breakpoints")) {
		t.Fatalf("unknown key kept: %s", out)
	}
	if !bytes.Contains(out, []byte(`"Transparent":false`)) {
		t.Fatalf("transparent false dropped: %s", out)
	}
	if !bytes.Contains(out, []byte(`"Y":-40.5`)) {
		t.Fatalf("coordinate dropped: %s", out)
	}
}

func TestUIDropsLegacyGroups(t *testing.T) {
	raw := []byte(`{
		"Groups": [{
			"ID": "ann_legacy",
			"X": 10,
			"Y": 20,
			"Width": 100,
			"Height": 80,
			"Title": "兼容测试"
		}]
	}`)
	ui, err := ParseUI(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ui.StickyNotes) != 0 {
		t.Fatalf("legacy groups retained: %+v", ui)
	}
}

func TestUIRejectsIllegalFields(t *testing.T) {
	if _, err := ParseUI([]byte(`{"ExecutionMode":"invalid"}`)); err == nil || !strings.Contains(err.Error(), "ExecutionMode") {
		t.Fatal(err)
	}
	if _, err := ParseUI([]byte(`{"EndpointSides":{"n":{"In":"middle"}}}`)); err == nil || !strings.Contains(err.Error(), "side") {
		t.Fatal(err)
	}
	if err := (UI{StickyNotes: []StickyNote{{ID: "a", Width: 10, Height: 10}, {ID: "a", Width: 10, Height: 10}}}).Validate(); err == nil {
		t.Fatal("duplicate sticky note must be rejected")
	}
	var ui UI
	if err := json.Unmarshal([]byte(`{"Positions":{"t":[0,0]}}`), &ui); err == nil {
		t.Fatal("position arrays are not Point")
	}
}

// TestUIEndpointSides 覆盖端点所在边与回边轨道的读取校验：只检查端点名与取值，
// 是否写全、入出是否同边在保存时由 node.Registry.CleanEndpointSides 检查。
func TestUIEndpointSides(t *testing.T) {
	accept := []string{
		`{"EndpointSides":{"n":{"In":"top","Out":"bottom","Error":"right"}}}`,
		`{"EndpointSides":{"n":{"In":"left","Out":"left"}}}`, // 读取不判断入出同边（不知道哪些键是入口）
		`{"EndpointSides":{"n":{"Out":"bottom"}}}`,
		`{"EdgeRails":{"e1":"left","e2":"right","e3":"top","e4":"bottom"}}`,
	}
	for _, raw := range accept {
		if _, err := ParseUI([]byte(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	reject := map[string]string{
		`{"EndpointSides":{"n":{"Error":""}}}`:                 "side",
		`{"EndpointSides":{"n":{"Error":"center"}}}`:           "side",
		`{"EndpointSides":{"n":{"bad id":"left"}}}`:            "endpoint",
		`{"EndpointSides":{"n":{"Out":1}}}`:                    "string",
		`{"EndpointSides":{"n":{"Sides":{"Error":"bottom"}}}}`: "string",
		`{"EdgeRails":{"e1":"above"}}`:                         "rail",
		`{"EdgeRails":{"e1":""}}`:                              "rail",
	}
	for raw, want := range reject {
		if _, err := ParseUI([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want error containing %q, got %v", raw, want, err)
		}
	}
}

// TestUISubLayouts 覆盖内嵌子图布局（SubLayouts 值为 Layout）：坐标与收起状态往返保留，嵌套层的句柄与轨道同样受校验。
func TestUISubLayouts(t *testing.T) {
	raw := `{"SubLayouts":{"lp":{"Collapsed":true,"Positions":{"start":{"X":24,"Y":56}},"SubLayouts":{"inner":{"EdgeRails":{"e1":"bottom"}}}}}}`
	ui, err := ParseUI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	body := ui.SubLayouts["lp"]
	if !body.Collapsed || body.Positions["start"] != (Point{X: 24, Y: 56}) || body.SubLayouts["inner"].EdgeRails["e1"] != "bottom" {
		t.Fatalf("sub layouts not preserved: %+v", ui.SubLayouts)
	}
	out, err := MarshalUI(ui)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(`"Bodies"`)) || !bytes.Contains(out, []byte(`"SubLayouts"`)) {
		t.Fatalf("sub layouts key: %s", out)
	}
	if again, err := ParseUI(out); err != nil || !again.SubLayouts["lp"].Collapsed {
		t.Fatalf("round trip: %s %v", out, err)
	}
	// 未知键丢弃，旧名 Bodies 不进入 SubLayouts。
	dropped, err := ParseUI([]byte(`{"Bodies":{"lp":{"Collapsed":true}}}`))
	if err != nil || len(dropped.SubLayouts) != 0 {
		t.Fatalf("unknown key kept: %+v %v", dropped.SubLayouts, err)
	}
	// Collapsed 只对 SubLayouts 中的子图层有意义：主图层同名键由 CleanUI 清掉，不进入规范化 JSON（不影响 HashContent）。
	top, err := ParseUI([]byte(`{"Collapsed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := MarshalUI(top); err != nil || string(out) != "{}" {
		t.Fatalf("top-level Collapsed: %s %v", out, err)
	}
	reject := map[string]string{
		`{"SubLayouts":{"lp":{"EdgeRails":{"e1":"middle"}}}}`:                                         "rail",
		`{"SubLayouts":{"lp":{"SubLayouts":{"x":{"EndpointSides":{"n":{"In":"left","Out":"up"}}}}}}}`: "side",
	}
	for raw, want := range reject {
		if _, err := ParseUI([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want error containing %q, got %v", raw, want, err)
		}
	}
}
