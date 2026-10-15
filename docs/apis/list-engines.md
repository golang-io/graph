# 列出执行引擎

返回已注册的执行引擎名，按名称排序。启动 Task 与设置默认引擎时只能使用这些值。

- 路径：`/engines`
- Method：`GET`

## 请求

无请求体、无查询参数。

## 响应

`200 OK`：`{"Data": string[]}`。两个引擎的区别见 [README](README.md#执行引擎)。

## 错误码

无业务错误码。

## 示例

```bash
curl -s http://127.0.0.1:8080/api/engines
```

```json
{"Data": ["durable", "immediate"]}
```
