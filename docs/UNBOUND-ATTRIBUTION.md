# 未绑符号归因表（Unbound Attribution）

状态：**active** · 机器判据：`scripts/unbound_attribution.py`（rc 0/1/2/3）

本表给**每一个未实现符号所在的库**一个闭集归因。口径包含两类：真正没绑定的
（当前 0 个）和以 `stub` 绑定的（211 个，见 `plugins/corpus_stubs/`）。stub 只让
语料「能链接、能加载、调用即显式失败」，不是实现，因此仍然必须归因、仍然计入
本表。目的不是「消灭 unbound 数字」，而是让每个缺口变成可排序的工单：哪些是
本地能做的、哪些是网络/服务端/schema 缺口、哪些是本仓环境没有的能力。
**新增未实现符号必须同时给出归因**，否则判红。
`audio_align_free` / `audio_subtitle_align_alt` 已由 `plugins/align_toolkit` 以 partial 实现（只收 duration_list，无音频内容可对齐）。
当前 `local-todo` 有 4 个符号：`audio_timeline_merge.timeline_merge`、`video_image_split` 的 2 个，
以及 `script_storyboard.Text_Shot`。缺口分别是 gap 插入位置/槽位顺序与分镜切分规则，
都是证据缺口，不是实现难度。

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
  audio_subtitle_align:
    class: remote-api
    evidence: "header desc「字幕音频自动对齐」表明需要远程服务"
  audio_subtitle_align_pro:
    class: remote-api
    evidence: "header desc「字幕音频对齐」表明需要远程服务"
  audio_timeline_merge:
    class: local-todo
    evidence: "纯时间线算术已确认；语料 JS（7535503584906215458:247）以 all_timeline[0].start 为基准，要求 pre 是 all 的前缀；gap_us_timeline 与 mp3_urls 一一对应（每个 gap 一段）。仍未证实 gap 的插入位置（pre/main 之间 还是 main 段之间）与 all_* 的包含关系，猜错会静默污染下游时间线，故暂不实现"
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
    evidence: "语料调用的 *_infos / keyframes_infos / timeline_keyframe / asr_timelines 已全部由 plugins/timeline_toolkit 实现（partial，loss 见清单）；audio_timelines 已由 plugins/capcut_data 实现（本地 WAV，partial）；剩余符号见符号级覆盖"
    symbols:
      search_sticker:
        class: remote-api
        evidence: "贴纸搜索走平台素材服务（网络/凭证）；原库级归因为 schema-unknown，已纠正"
  capcut_draft:
    class: server-state
    evidence: "与 D-1 同族：草稿创建/保存；string_to_list 是纯字符串助手，已由 plugins/text_toolkit 实现（partial），不随库级归因"
  capcut_draft_data:
    class: schema-unknown
    evidence: "timelines / str_to_list / caption_splitter / get_audio_duration / audio_timelines / audio_infos / image_infos 已由 plugins/timeline_toolkit 实现（partial，loss 见清单；audio/image 的键由语料手写的 add_audios/add_images infos 结构固定；get_audio_duration 只解码本地 WAV，单位微秒）；text_infos / video_infos / keyframe_infos 的消费面无手写结构，schema 未证实"
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
    evidence: "video(model / first_frame_url / resolution) 生成视频；语料 7595856252548448298 传 doubao-seedance-pro 并消费返回的视频 URL——远程视频生成服务"
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
    class: remote-api
    evidence: "素材库检索走平台素材服务（网络/凭证）；原归因写「环境缺失」，实为 remote-api，已纠正"
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
  html_upload:
    class: remote-api
    evidence: "header desc「发布网页」表明需要远程服务"
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
    evidence: "mihewan3(key, prompt, images, videos) 生成 + task_info(id) 轮询；key 是服务凭证，属远程视频生成"
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
    class: local-todo
    evidence: "Text_Shot(num, text) 只做纯文本分镜、无 URL/凭证；num 的取值为 6/12/25/缺省（4 个调用点），切分规则无语料手写等价物，保持未实现"
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
    evidence: "剩余 audio_timelines/video_timelines 需要服务端音频/视频探测；get_audio_duration 已由 plugins/compose_toolkit 以本地 WAV + unit 换算实现（partial）；其余纯函数（含 timeline_align）同插件绑定"
  suno_api:
    class: remote-api
    evidence: "header desc「suno_api」表明需要远程服务"
  super_table:
    class: remote-api
    evidence: "get_url / str_list 是纯 wire 助手，已由 plugins/table_toolkit 实现（语料把 get_url 用在 prompt/text/ID 等标量上，说明不是远程取链）；get_table / save_list / super_table 需要表格服务（网络/凭证）"
  taobao_product:
    class: remote-api
    evidence: "header desc「淘宝商品数据」表明需要远程服务"
  text_audio_align:
    class: remote-api
    evidence: "header desc「文本音频对齐」表明需要远程服务"
  text_image_compose:
    class: remote-api
    evidence: "html_to_image(api_key, html_str) 带 api_key，渲染在平台服务端完成；原库级归因为 schema-unknown，已纠正"
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
    evidence: "add_veo3_video_task / get_veo3_video 带 api_key + task_id 轮询：远程视频生成服务"
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
    class: local-todo
    evidence: "video_imge_split 把文案拆成 image_text/video_text 并给出计数；Video_Image_Merging 用 image_num/video_num 把每条 timeline 切成图片/视频槽位。槽位顺序与 image_Arr_String 的内容无语料证据，暂不实现"
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
