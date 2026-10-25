// compose.go — capcut_video_compose 的开源实现：**只有** get_audio_duration。
//
// 其余 12 个函数（create_draft / add_* / save_draft / gen_video / …）是**服务端
// 草稿状态机**：draft_url / draft_id 是服务句柄而不是文件地址——
//
//	· 语料把上一次返回的 draft_url 直接塞进下一次的 draft_id 继续变更草稿；
//	· 最终 `return v13.draft_id` 交给「剪映小助手」下载草稿；
//	· gen_video 需要 api_token 做服务端渲染。
//
// 因此本仓没有可验证的本地等价物，故意保持 unbound（见
// docs/LIBRARY-BINDING-SPEC.md §15 D-1），不用本地文件冒充草稿。
//
// get_audio_duration 例外：它是纯本地解码，且语料把它当**微秒整数**用
// （时间线端点 / 关键帧 offset 直接用，没有 ×1e6 换算）。
package localtools

import (
	"context"
	"fmt"
	"math"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

func registerCapcutVideoCompose(m *plugin.Manager, opts Options) error {
	if !opts.allows("capcut_video_compose") {
		return nil
	}
	p := &plugin.Plugin{
		ID: "local_capcut_video_compose", Version: "0.1.0", Type: plugin.PluginCallable,
		Tools: []plugin.Tool{{
			Name:         "get_audio_duration",
			Desc:         "本地音频时长，**微秒整数**（WAV/MP3/OGG 精确 / 不联网）",
			Capabilities: []string{"capcut_video_compose.get_audio_duration"},
			Call:         composeAudioDuration,
		}},
	}
	return m.Register(p)
}

// composeAudioDuration 返回 header 声明的三个键：duration / message / outputList。
// 失败返回 Go error（header 的 @capability failure_branch=true 会接住），
// 因为该结果类型没有 isSuccess/errorBody 可用于表达可恢复失败。
func composeAudioDuration(_ context.Context, args map[string]any) (any, error) {
	raw := args["mp3_url"]
	if raw == nil || fmt.Sprint(raw) == "" {
		return nil, fmt.Errorf("MISSING_INPUT: mp3_url is required")
	}
	data, err := audioBytes(raw)
	if err != nil {
		return nil, err
	}
	seconds, format, err := audioDuration(data)
	if err != nil {
		return nil, err
	}
	us := float64(math.Round(seconds * 1e6))
	msg := fmt.Sprintf("local %s: %.3fs", format, seconds)
	return map[string]any{
		"duration":   us,
		"message":    msg,
		"outputList": []any{map[string]any{"duration": us, "message": msg}},
	}, nil
}
