/**
 * api — 示例前端访问 graph HTTP 接口（前缀 /api）的客户端。
 * 请求与响应的键名与服务端 Go 字段一致（大驼峰）；列表响应为 {Data}，错误响应为 {Error, Code}。
 */
import type {
  GraphDefinition,
  GraphNodeSpec,
  GraphResumeRequest,
  GraphTaskPolicy,
  GraphTaskSnapshot,
  GraphTaskSummary,
  GraphUI,
} from '@golang-io/graph'

/** 列表响应的外层结构。 */
type List<T> = { Data: T[] }

/**
 * POST /tasks 与控制动作（resume / pause / cancel）的回执。
 * Version 是 Task 写回计数；GraphVersion 是执行的图定义版本号（执行临时图时省略）。
 * 对已结束的 Task 发控制动作时返回 202，Status 为当前终态。
 */
export type Receipt = { TaskID: string; EngineKind: string; Status?: string; Version?: number; GraphVersion?: number }

/** 优先展示服务端返回的 {Error, Code}，否则回退为状态文本。 */
async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  if (!res.ok) {
    const text = await res.text()
    let message = text || res.statusText
    try {
      const body = JSON.parse(text) as { Error?: string; Code?: string }
      if (body.Error) message = body.Code ? `${body.Code}: ${body.Error}` : body.Error
    } catch {
      // 非 JSON 错误体（如代理返回的 HTML）原样展示。
    }
    throw new Error(message)
  }
  return (await res.json()) as T
}

function send<T>(url: string, method: string, body: unknown): Promise<T> {
  return request<T>(url, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

const graphURL = (id: string) => `/api/graphs/${encodeURIComponent(id)}`
const taskURL = (id: string) => `/api/tasks/${encodeURIComponent(id)}`

export const api = {
  graphs: () => request<List<GraphDefinition>>('/api/graphs').then((r) => r.Data),
  graph: (id: string) => request<GraphDefinition>(graphURL(id)),
  /** 保存只提交可编辑字段（名称、描述、图、布局）；版本号、哈希、时间由服务端生成。 */
  save: (id: string, def: Pick<GraphDefinition, 'Name' | 'Description'> & GraphUI) =>
    send<GraphDefinition>(graphURL(id), 'PUT', {
      Name: def.Name,
      Description: def.Description,
      Graph: def.Graph,
      UI: def.UI,
    }),
  nodeSpecs: () => request<List<GraphNodeSpec>>('/api/node-specs').then((r) => r.Data),
  engines: () => request<List<string>>('/api/engines').then((r) => r.Data),
  startTask: (body: {
    GraphID: string
    Engine: string
    Input: Record<string, unknown>
    Policy?: GraphTaskPolicy
  }) => send<Receipt>('/api/tasks', 'POST', body),
  /** 查询 Task 历史列表（按 StartedAt 倒序），支持按 GraphID / Status 过滤。 */
  tasks: (query?: { GraphID?: string; Status?: string; Limit?: number; After?: string }) => {
    const sp = new URLSearchParams()
    if (query?.GraphID) sp.set('GraphID', query.GraphID)
    if (query?.Status) sp.set('Status', query.Status)
    if (query?.Limit) sp.set('Limit', String(query.Limit))
    if (query?.After) sp.set('After', query.After)
    const qs = sp.toString()
    return request<List<GraphTaskSummary>>(qs ? `/api/tasks?${qs}` : '/api/tasks').then((r) => r.Data)
  },
  snapshot: (taskID: string) => request<GraphTaskSnapshot>(taskURL(taskID)),
  /** 放行暂停、单步或断点上的 Run，或动态更新调试选项（POST /tasks/{id}/resume，空请求体即普通恢复）。 */
  resume: (taskID: string, req?: GraphResumeRequest) =>
    send<Receipt>(`${taskURL(taskID)}/resume`, 'POST', req ?? {}),
  /** 暂停正在运行的 Task（POST /tasks/{id}/pause）。 */
  pause: (taskID: string) => send<Receipt>(`${taskURL(taskID)}/pause`, 'POST', {}),
  /** 取消未结束的 Task（POST /tasks/{id}/cancel）。 */
  cancel: (taskID: string, reason?: string) =>
    send<Receipt>(`${taskURL(taskID)}/cancel`, 'POST', reason ? { Reason: reason } : {}),
  /** 订阅 Task 快照（SSE）；每条消息的 data 是完整 TaskSnapshot。 */
  watchURL: (taskID: string) => `${taskURL(taskID)}/watch`,
}
