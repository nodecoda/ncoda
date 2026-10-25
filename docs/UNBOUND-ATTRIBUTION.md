# 未绑符号归因表（Unbound Attribution）

状态：**active** · 机器判据：`scripts/unbound_attribution.py`（rc 0/1/2/3）

本表给**每一个未绑定符号所在的库**一个闭集归因。目的不是「消灭 unbound」，
而是让 242 个未绑符号变成可排序的工单：哪些是本地能做的、哪些是网络/服务端/
schema 缺口、哪些是本仓环境没有的能力。**新增未绑库必须同时给出归因**，否则判红。当前 `local-todo` 已清零，剩余
全部是网络/服务端状态/schema/环境能力缺口。

## 归因闭集

| class | 含义 |
| --- | --- |
| `local-todo` | 纯本地可做，但语义还需取证（分隔符/取整/填充策略）；取证后可在 pkg/localtools 或 plugins/ 落地 |
| `remote-api` | 需要网络与/或凭证（搜索、云模型、SaaS、平台数据）；本仓不提供网络能力，保持 unbound |
| `server-state` | 需要服务端可变草稿状态（draft_url/draft_id 为句柄）；本地无等价物，见 §15 D-1 |
| `schema-unknown` | 需要未公开的数据 schema（剪映草稿 JSON 片段）；无证据前不实现，避免假绿 |
| `env-missing` | 需要本仓未提供的环境能力（ffmpeg / headless 浏览器 / 模型权重）；属「环境输入缺失」 |

## 表

`libraries` 按库归因；`evidence` 必须写清「凭什么这么判」。
库内语义不一致时用 `symbols: {fn: {class, evidence}}` 做**符号级覆盖**（优先于库级）；
覆盖的 library/function 必须真实存在于 capability header，写错判 rc=2。

```yaml
schema: ncoda.unbound-attribution/v1
libraries:
  adjustment:
    class: remote-api
    evidence: "change(brightness/contrast/saturation, image_url) 需要图像处理服务"
  ai302:
    class: remote-api
    evidence: "header desc「302AI」表明需要远程服务"
  arxiv_search:
    class: remote-api
    evidence: "header desc「arXiv」表明需要远程服务"
  asr:
    class: remote-api
    evidence: "header desc「语音识别」表明需要远程服务"
  audio_align:
    class: remote-api
    evidence: "header desc「字幕音频对齐」表明需要远程服务"
  audio_align_free:
    class: remote-api
    evidence: "header desc「字幕音频对齐_免费版」表明需要远程服务"
  audio_subtitle_align:
    class: remote-api
    evidence: "header desc「字幕音频自动对齐」表明需要远程服务"
  audio_subtitle_align_alt:
    class: remote-api
    evidence: "header desc「音频字幕对齐」表明需要远程服务"
  audio_subtitle_align_pro:
    class: remote-api
    evidence: "header desc「字幕音频对齐」表明需要远程服务"
  audio_timeline_merge:
    class: local-todo
    evidence: "纯时间线算术（header 输入是 main_timeline/pre_timeline，不涉及音频解码）；10 个输出字段（all_* / gap_us_timeline / contact）的排布语义未由语料消费面证实，暂不实现。原归因写「需要解码音频时长」与 header 不符，已纠正"
  audio_toolkit:
    class: env-missing
    evidence: "需要真实音频编解码（ffmpeg 级）；本仓只做 WAV 生成/时长读取"
  av_toolkit:
    class: env-missing
    evidence: "需要真实音视频编解码与封装"
  bgm_library:
    class: remote-api
    evidence: "header desc「背景音乐库」表明需要远程服务"
  bocha_search:
    class: remote-api
    evidence: "header desc「博查搜索社区版」表明需要远程服务"
  book_search_online:
    class: remote-api
    evidence: "header desc「在线搜书」表明需要远程服务"
  byteartist:
    class: remote-api
    evidence: "header desc「ByteArtist」表明需要远程服务"
  capcut:
    class: server-state
    evidence: "与 D-1 同族：剪映小助手 create_draft/add_*/save_draft"
  capcut_data:
    class: schema-unknown
    evidence: "输出 CapCut 草稿 JSON 片段；9 个纯函数已由 plugins/capcut_data 实现（get_url/objs_to_str_list/str_to_list/array_pair/extract_first_end_frame/timelines/wenan_links_merge/wenan_split/timelines_format），其余 *_infos/keyframes_infos/search_sticker 无语料可证 schema；timeline_keyframe / asr_timelines 已由 plugins/timeline_toolkit 实现（partial，loss 见清单）"
  capcut_data_fast:
    class: schema-unknown
    evidence: "与 capcut_data 同族的 *_infos 生成器：草稿 JSON 片段，无 schema 证据"
  capcut_draft:
    class: server-state
    evidence: "与 D-1 同族：草稿创建/保存"
  capcut_draft_data:
    class: schema-unknown
    evidence: "timelines / str_to_list / caption_splitter 已由 plugins/timeline_toolkit 实现（partial，分配/断句规则 loss 见清单）；audio_infos / text_infos / image_infos / keyframe_infos / video_infos 返回草稿 JSON 片段，无 schema 证据"
  capcut_draft_folder:
    class: server-state
    evidence: "与 D-1 同族：草稿文件夹（add_* 以 draft id 串联）"
  capcut_draft_gen:
    class: server-state
    evidence: "与 D-1 同族：一键生成草稿"
  capcut_keyframes:
    class: server-state
    evidence: "与 D-1 同族：关键帧写入已有草稿"
  capcut_resources:
    class: remote-api
    evidence: "header desc「剪映资源」表明需要远程服务"
  capcut_video_compose:
    class: server-state
    evidence: "见 §15 D-1：draft_url/draft_id 是服务端草稿句柄；仅 get_audio_duration 已实现（微秒）"
  capcut_video_fast:
    class: server-state
    evidence: "与 D-1 同族：create_draft/add_*/save_draft 串行变更服务端草稿"
  caption_animation:
    class: schema-unknown
    evidence: "animation_change(input: string) 的输入/输出都是字幕 JSON 字符串，schema 未公开"
  codel_video:
    class: remote-api
    evidence: "语料只调用 keyframe_ani_scene_v1：收 api_token、返回 tip_url（corpus/7576180544199933987_S117hb_shangye.ncoda:838），是远程服务；同库 split_list/merge_list 无语料调用但已由 plugins/codel_toolkit 按 header 形状实现（partial）"
  copy_check:
    class: remote-api
    evidence: "content_check 收 api_key/model，是模型服务"
  doc_maker:
    class: remote-api
    evidence: "header desc「Doc Maker」表明需要远程服务"
  douban_book_search:
    class: remote-api
    evidence: "header desc「豆瓣搜书」表明需要远程服务"
  doubao_image_gen:
    class: remote-api
    evidence: "header desc「豆包图像生成大模型」表明需要远程服务"
  doubao_img2video:
    class: remote-api
    evidence: "header desc「豆包图生视频」表明需要远程服务"
  doubao_seedream45:
    class: remote-api
    evidence: "header desc「Doubao-Seedream-4.5（自接入版）」表明需要远程服务"
  doubao_video_gen:
    class: remote-api
    evidence: "header desc「豆包图生视频」表明需要远程服务"
  doubao_video_lite:
    class: remote-api
    evidence: "header desc「(corpus fallback: DB 快照未收录)」表明需要远程服务"
  douyin_copy:
    class: remote-api
    evidence: "header desc「抖音文案」表明需要远程服务"
  douyin_keyword_search:
    class: remote-api
    evidence: "header desc「抖音关键词检索」表明需要远程服务"
  douyin_video_get:
    class: remote-api
    evidence: "header desc「抖音视频获取」表明需要远程服务"
  dubbingx_tts:
    class: remote-api
    evidence: "header desc「文本转音频DubbingX」表明需要远程服务"
  dy_user_aweme_list:
    class: remote-api
    evidence: "header desc「获取博主作品列表」表明需要远程服务"
  dy_user_profile:
    class: remote-api
    evidence: "header desc「获取博主信息」表明需要远程服务"
  dy_video_search:
    class: remote-api
    evidence: "header desc「视频搜索」表明需要远程服务"
  edit_material_lib:
    class: env-missing
    evidence: "素材库操作依赖平台素材服务"
  face_swap:
    class: remote-api
    evidence: "header desc「智能换脸」表明需要远程服务"
  feishu_bitable:
    class: remote-api
    evidence: "header desc「飞书多维表格」表明需要远程服务"
  feishu_docs:
    class: remote-api
    evidence: "header desc「飞书云文档」表明需要远程服务"
  feiyin_digital_human:
    class: remote-api
    evidence: "header desc「飞影数字人」表明需要远程服务"
  fish_voice:
    class: remote-api
    evidence: "header desc「fish语音」表明需要远程服务"
  flux_kontext:
    class: remote-api
    evidence: "header desc「Flux_kontext最新图片模型」表明需要远程服务"
  gt2_image_gen:
    class: remote-api
    evidence: "header desc「GT2_图片生成」表明需要远程服务"
  hailuo_img2video:
    class: remote-api
    evidence: "header desc「海螺图生视频」表明需要远程服务"
  homepage_analyzer:
    class: remote-api
    evidence: "header desc「主页分析」表明需要远程服务"
  horse_video_gen:
    class: remote-api
    evidence: "header desc「Horse视频生成」表明需要远程服务"
  hotlist_aggregate:
    class: remote-api
    evidence: "header desc「热搜榜聚合」表明需要远程服务"
  html2img:
    class: env-missing
    evidence: "HTML→图片需要 headless 浏览器"
  html_upload:
    class: remote-api
    evidence: "header desc「发布网页」表明需要远程服务"
  htmlcode_to_img:
    class: env-missing
    evidence: "HTML 代码→图片需要 headless 浏览器"
  image_enhance:
    class: remote-api
    evidence: "header desc「画质提升」表明需要远程服务"
  image_to_video:
    class: remote-api
    evidence: "header desc「图片转视频」表明需要远程服务"
  image_understand:
    class: remote-api
    evidence: "header desc「图片理解」表明需要远程服务"
  instruction_edit:
    class: remote-api
    evidence: "header desc「指令编辑」表明需要远程服务"
  jimeng_img2video:
    class: remote-api
    evidence: "header desc「即梦图生视频」表明需要远程服务"
  jimeng_lip_sync:
    class: remote-api
    evidence: "header desc「即梦对口型」表明需要远程服务"
  jimeng_video_gen:
    class: remote-api
    evidence: "header desc「即梦视频生成」表明需要远程服务"
  kimi_chat_parser:
    class: remote-api
    evidence: "header desc「(corpus fallback: DB 快照未收录)」表明需要远程服务"
  kling_img2video:
    class: remote-api
    evidence: "header desc「可灵图生视频」表明需要远程服务"
  link_reader:
    class: remote-api
    evidence: "header desc「链接读取」表明需要远程服务"
  llm_asr:
    class: remote-api
    evidence: "header desc「大模型语音识别」表明需要远程服务"
  manbo_tts:
    class: remote-api
    evidence: "header desc「语音合成_速创小助手 曼波配音」表明需要远程服务"
  media_resources:
    class: remote-api
    evidence: "header desc「资源素材」表明需要远程服务"
  mihewan3:
    class: remote-api
    evidence: "header desc「(corpus fallback: DB 快照未收录)」表明需要远程服务"
  mivideo_fast:
    class: remote-api
    evidence: "header desc「MIVIDEO生成快速版」表明需要远程服务"
  mivideo_slow:
    class: remote-api
    evidence: "header desc「MIVIDEO生成慢速版」表明需要远程服务"
  music_generate:
    class: remote-api
    evidence: "header desc「音乐生成」表明需要远程服务"
  na2_image_gen:
    class: remote-api
    evidence: "header desc「na2图片生成」表明需要远程服务"
  na_image_gen:
    class: remote-api
    evidence: "header desc「na图片生成」表明需要远程服务"
  ocr:
    class: remote-api
    evidence: "header desc「OCR」表明需要远程服务"
  ocr_volcano:
    class: remote-api
    evidence: "header desc「OCR火山版」表明需要远程服务"
  paraformer_asr:
    class: remote-api
    evidence: "header desc「阿里云百炼Paraformer语音识别功能」表明需要远程服务"
  prompt_optimize:
    class: remote-api
    evidence: "header desc「提示词优化」表明需要远程服务"
  qwen_vl:
    class: remote-api
    evidence: "header desc「通义千问」表明需要远程服务"
  script_storyboard:
    class: remote-api
    evidence: "header desc「文案自动分镜」表明需要远程服务"
  secondhand_goods:
    class: remote-api
    evidence: "header desc「二手物资」表明需要远程服务"
  seedance2_video:
    class: remote-api
    evidence: "header desc「即梦AI-Seedance2.0」表明需要远程服务"
  seedream3_text2img:
    class: remote-api
    evidence: "header desc「文生图_seedream3」表明需要远程服务"
  smart_draw:
    class: remote-api
    evidence: "header desc「智能绘图_文生图」表明需要远程服务"
  so2_video_gen:
    class: remote-api
    evidence: "header desc「米核视频生成」表明需要远程服务"
  so_video_gen:
    class: remote-api
    evidence: "header desc「SO视频生成」表明需要远程服务"
  sound_effect_lib:
    class: remote-api
    evidence: "header desc「音效库」表明需要远程服务"
  sound_search:
    class: remote-api
    evidence: "header desc「搜索音效」表明需要远程服务"
  speech_to_text:
    class: remote-api
    evidence: "header desc「语音转文本」表明需要远程服务"
  stickman_search:
    class: remote-api
    evidence: "header desc「火柴人矢量图搜索」表明需要远程服务"
  subtitle_get:
    class: remote-api
    evidence: "header desc「字幕获取」表明需要远程服务"
  suchuang_video_compose:
    class: server-state
    evidence: "剩余 audio_timelines/get_audio_duration/video_timelines 需要服务端音频/视频探测；其余纯函数（含 timeline_align）已由 plugins/compose_toolkit 绑定"
  suno_api:
    class: remote-api
    evidence: "header desc「suno_api」表明需要远程服务"
  super_table:
    class: remote-api
    evidence: "header desc「超级多维表格」表明需要远程服务"
  taobao_product:
    class: remote-api
    evidence: "header desc「淘宝商品数据」表明需要远程服务"
  text_audio_align:
    class: remote-api
    evidence: "header desc「文本音频对齐」表明需要远程服务"
  text_image_compose:
    class: schema-unknown
    evidence: "图文合成，产物是草稿片段/草稿 id（同 D-1 族）"
  tianyancha:
    class: remote-api
    evidence: "header desc「天眼查」表明需要远程服务"
  timer_wait_dog:
    class: server-state
    evidence: "wait(workflow_id/api_token/callback_workflow_id) 轮询服务端工作流，返回 data.web_url 给人继续操作（corpus/7531651401567453225_X166_Vgushici_doubao_1_1.ncoda:140-141 直接 return v14.data.web_url）；本地无等价物"
  toutiao_image_search:
    class: remote-api
    evidence: "header desc「头条图片搜索」表明需要远程服务"
  toutiao_news:
    class: remote-api
    evidence: "header desc「头条新闻」表明需要远程服务"
  toutiao_search:
    class: remote-api
    evidence: "header desc「头条搜索」表明需要远程服务"
  veo3_video:
    class: remote-api
    evidence: "header desc「(corpus fallback: DB 快照未收录)」表明需要远程服务"
  video2audio:
    class: env-missing
    evidence: "转码需要 ffmpeg"
  video_all_download:
    class: remote-api
    evidence: "header desc「视频全平台下载」表明需要远程服务"
  video_asr:
    class: remote-api
    evidence: "header desc「视频语音转文案」表明需要远程服务"
  video_audio_extractor:
    class: env-missing
    evidence: "音轨抽取需要 ffmpeg"
  video_compose_toolkit:
    class: env-missing
    evidence: "需要真实视频合成（含上传），本仓不带 ffmpeg/对象存储"
  video_download:
    class: remote-api
    evidence: "header desc「视频下载」表明需要远程服务"
  video_edit_tool:
    class: env-missing
    evidence: "需要真实视频剪辑（concat/trim/字幕烧录），本仓不带 ffmpeg"
  video_enhance:
    class: remote-api
    evidence: "header desc「视频增强」表明需要远程服务"
  video_frame_extractor:
    class: env-missing
    evidence: "抽帧需要 ffmpeg"
  video_generation:
    class: remote-api
    evidence: "header desc「视频生成（内置节点 doubao-seedance;动态输入 + 静态配置透传,输出 video）」表明需要远程服务"
  video_image_split:
    class: env-missing
    evidence: "视频/图片拆分需要真实媒体处理"
  video_montage:
    class: env-missing
    evidence: "混剪需要真实媒体处理"
  video_nowm_download:
    class: remote-api
    evidence: "header desc「视频无水印下载」表明需要远程服务"
  video_nowm_parse:
    class: remote-api
    evidence: "header desc「视频无水印解析」表明需要远程服务"
  video_parse:
    class: remote-api
    evidence: "header desc「抖音视频解析」表明需要远程服务"
  video_search:
    class: remote-api
    evidence: "header desc「视频搜索」表明需要远程服务"
  video_text_extract:
    class: remote-api
    evidence: "header desc「视频文本提取」表明需要远程服务"
  video_tool:
    class: env-missing
    evidence: "需要真实抽帧/音频抽取（ffmpeg）"
  vidu_img2video:
    class: remote-api
    evidence: "header desc「vidu图生视频」表明需要远程服务"
  viral_video_prompt:
    class: remote-api
    evidence: "header desc「爆款视频二创提示词」表明需要远程服务"
  voice_clone:
    class: remote-api
    evidence: "header desc「声音克隆」表明需要远程服务"
  wechat_mp:
    class: remote-api
    evidence: "header desc「微信公众号API」表明需要远程服务"
  wujie:
    class: remote-api
    evidence: "header desc「全球大模型」表明需要远程服务"
  xhs_article_extract:
    class: remote-api
    evidence: "header desc「小红书文章信息提取」表明需要远程服务"
  xhs_note_get:
    class: remote-api
    evidence: "header desc「小红书笔记提取」表明需要远程服务"
  xhs_note_search:
    class: remote-api
    evidence: "header desc「小红书笔记搜索」表明需要远程服务"
  xhs_search_note:
    class: remote-api
    evidence: "header desc「小红书」表明需要远程服务"
  xhs_user_note:
    class: remote-api
    evidence: "header desc「小红书工具」表明需要远程服务"
  zh_tts:
    class: remote-api
    evidence: "header desc「中文文本转语音」表明需要远程服务"
```

## 统计（由脚本生成，勿手改）

```bash
python3 scripts/unbound_attribution.py           # 人读
python3 scripts/unbound_attribution.py --check   # 棘轮：未分类符号不得增加
```
