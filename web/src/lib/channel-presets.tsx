import type { ComponentType } from 'react';
import type { SvgIconProps } from '@thesvg/react';
import NewAPIIcon from '@thesvg/react/new-api';
import OpenAIIcon from '@thesvg/react/openai-chatgpt';
import AnthropicIcon from '@thesvg/react/anthropic';
import VolcengineIcon from '@thesvg/react/volcengine';
import DeepSeekIcon from '@thesvg/react/deepseek';
import OpenRouterIcon from '@thesvg/react/openrouter';
import GroqIcon from '@thesvg/react/groq';
import QwenIcon from '@thesvg/react/qwen';
import MoonshotIcon from '@thesvg/react/moonshot-ai';
import ZhipuIcon from '@thesvg/react/zhipu';
import XAIIcon from '@thesvg/react/xai-grok';
import SiliconFlowIcon from '@thesvg/react/siliconcloud-siliconflow';
import AzureIcon from '@thesvg/react/azure-azure-openai';
// Gemini API 与 Antigravity 都有现成图标（后者正是 Google 那条 Cloud Code 路线）。
import GeminiIcon from '@thesvg/react/ai-studio-google';
import AntigravityIcon from '@thesvg/react/antigravity-google';
// 厂商方言里没有现成品牌图标的（如 NanoGPT）用下面这个中性占位图标，不拿别家的顶上。
import CerebrasIcon from '@thesvg/react/cerebras';
import ClineIcon from '@thesvg/react/cline';
import FireworksIcon from '@thesvg/react/fireworks';
import LongCatIcon from '@thesvg/react/longcat';
import ModelScopeIcon from '@thesvg/react/modelscope';
import OpenCodeIcon from '@thesvg/react/opencode';
import OllamaIcon from '@thesvg/react/ollama';
import type { Dialect } from '@/api/channel';
import { GenericVendorIcon } from './generic-vendor-icon';

// ChannelPreset 是服务商的地址, 路径与方言预填模板。
// 地址与路径只在前端存在, 不落库: 这些服务商对后端没有区别, 只是地址和路径不同。
// 方言随模板给出: 它表达的是该服务商在标准协议之上的差异, 属于服务商固有属性, 由用户另选没有意义。
export type ChannelPreset = {
    id: string;
    label: string;
    Icon: ComponentType<SvgIconProps>;
    iconClassName?: string; // 单色图标在深色主题下需反色。
    dialect: Dialect;
    base_url: string;
    openai_chat_completion_path: string;
    openai_response_path: string;
    anthropic_message_path: string;
    // gemini_project 只有 antigravity 方言需要（Cloud Code PA 用它定位计费项目）。
    // 不预填：它是使用者自己的 Google Cloud 项目 ID，不是服务商固有属性。
    gemini_project?: string;
};

// 默认协议路径, 与后端 DDL 默认值一致。
const CHAT = '/v1/chat/completions';
const RESP = '/v1/responses';
const ANTH = '/v1/messages';

export const CHANNEL_PRESETS: ChannelPreset[] = [
    {
        id: 'newapi', label: 'New API', Icon: NewAPIIcon,
        dialect: 'generic',
        base_url: '',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'openai', label: 'OpenAI', Icon: OpenAIIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'generic',
        base_url: 'https://api.openai.com',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'anthropic', label: 'Anthropic', Icon: AnthropicIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'generic',
        base_url: 'https://api.anthropic.com',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'volcengine', label: '火山方舟', Icon: VolcengineIcon,
        dialect: 'doubao',
        base_url: 'https://ark.cn-beijing.volces.com/api/v3',
        openai_chat_completion_path: '/chat/completions', openai_response_path: '/responses', anthropic_message_path: '/messages',
    },
    {
        id: 'deepseek', label: 'DeepSeek', Icon: DeepSeekIcon,
        dialect: 'deepseek',
        base_url: 'https://api.deepseek.com/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'openrouter', label: 'OpenRouter', Icon: OpenRouterIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'openrouter',
        base_url: 'https://openrouter.ai/api/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'groq', label: 'Groq', Icon: GroqIcon,
        dialect: 'generic',
        base_url: 'https://api.groq.com/openai',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'dashscope', label: '通义千问', Icon: QwenIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'bailian',
        base_url: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'moonshot', label: 'Moonshot', Icon: MoonshotIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'moonshot',
        base_url: 'https://api.moonshot.cn/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'zhipu', label: '智谱 GLM', Icon: ZhipuIcon,
        dialect: 'zai',
        base_url: 'https://open.bigmodel.cn/api/paas/v4',
        openai_chat_completion_path: '/chat/completions', openai_response_path: '/responses', anthropic_message_path: '/messages',
    },
    {
        id: 'xai', label: 'xAI', Icon: XAIIcon, iconClassName: 'brightness-0 dark:invert',
        dialect: 'xai',
        base_url: 'https://api.x.ai/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'siliconflow', label: 'SiliconFlow', Icon: SiliconFlowIcon,
        dialect: 'generic',
        base_url: 'https://api.siliconflow.cn',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'azure', label: 'Azure OpenAI', Icon: AzureIcon,
        dialect: 'generic',
        base_url: '',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    // Gemini 走**原生协议**（/v1beta/models/{model}:generateContent），不是 OpenAI 兼容端点，
    // 所以这里给的是服务根：完整路径含模型名，由后端转换器拼，前端没有路径字段可填。
    {
        id: 'gemini', label: 'Google Gemini', Icon: GeminiIcon,
        dialect: 'generic',
        base_url: 'https://generativelanguage.googleapis.com',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    // Antigravity = Google Cloud Code PA：线协议同样是 Gemini，差别在端点由后端转换器自选
    // （prod / daily / autopush 三个 sandbox 主机）并要套它自己的信封与报文清理，
    // 因此方言必须是 antigravity —— 选错了会打到公开的 Gemini 端点上，拿不到 Cloud Code 的能力。
    {
        id: 'antigravity', label: 'Antigravity', Icon: AntigravityIcon,
        dialect: 'antigravity',
        base_url: 'https://cloudcode-pa.googleapis.com',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    // ── OpenAI 线协议 + 厂商方言 ──
    //
    // 这一组的线协议与上面几家完全相同（都是 OpenAI Chat Completions），差别在报文归一化
    // 与端点规则，由后端按 dialect 选转换器。**每个方言都必须有一个预设**：
    // 表单里没有方言选择器，dialect 只能由预设带出，漏一家的后果不是"不好选"
    // 而是"界面上根本设不出来"。
    //
    // base_url 一律写到**版本级**（/v1 之类），不要贴完整端点 ——
    // 贴了会得到 .../chat/completions/chat/completions（实测踩过）。
    // openai_chat_completion_path 对厂商方言是**惰性**的（转换器自带端点规则、结构上不读它），
    // 这里仍填默认值只为满足类型，真正生效的是 base_url。
    {
        id: 'cerebras', label: 'Cerebras', Icon: CerebrasIcon,
        dialect: 'cerebras',
        base_url: 'https://api.cerebras.ai/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'cline', label: 'Cline', Icon: ClineIcon,
        dialect: 'cline',
        base_url: 'https://api.cline.bot/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'fireworks', label: 'Fireworks', Icon: FireworksIcon,
        dialect: 'fireworks',
        base_url: 'https://api.fireworks.ai/inference/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'longcat', label: 'LongCat', Icon: LongCatIcon,
        dialect: 'longcat',
        base_url: 'https://api.longcat.chat/openai/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'modelscope', label: 'ModelScope', Icon: ModelScopeIcon,
        dialect: 'modelscope',
        base_url: 'https://api-inference.modelscope.cn/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'nanogpt', label: 'NanoGPT', Icon: GenericVendorIcon,
        dialect: 'nanogpt',
        base_url: 'https://nano-gpt.com/api/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    {
        id: 'opencode', label: 'OpenCode', Icon: OpenCodeIcon,
        dialect: 'opencode',
        base_url: 'https://opencode.ai/zen/v1',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
    // Ollama 是**独立线协议**（/api/chat，实测 APIFormat 是 ollama/chat），不是 OpenAI 方言：
    // 所以它的 dialect 保持 generic，靠**协议位**表达 —— 建渠道时要在授权矩阵里勾 ollama 那一列。
    // 方言与协议位是两件事，别在这里填 dialect（填了也没用，后端只按协议位分派）。
    {
        id: 'ollama', label: 'Ollama', Icon: OllamaIcon,
        dialect: 'generic',
        base_url: 'http://127.0.0.1:11434',
        openai_chat_completion_path: CHAT, openai_response_path: RESP, anthropic_message_path: ANTH,
    },
];
