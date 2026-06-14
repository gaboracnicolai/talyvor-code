// LensClient is the tiny HTTP wrapper for the Talyvor Lens
// `/v1/proxy/anthropic/v1/messages` endpoint. Uses the JDK's
// java.net.http.HttpClient — no external dep beyond org.json
// (declared in build.gradle.kts).
//
// Every call carries the X-Talyvor-{Feature,Workspace,Issue}
// headers so Lens can attribute spend to the active Track issue
// — same contract as the VS Code extension and the CLI agent.

package com.talyvor.code

import com.talyvor.code.lens.SseBuffer
import com.talyvor.code.lens.SsePure
import com.talyvor.code.lens.StreamEventKind
import com.talyvor.code.lens.StreamProvider
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration

class LensClient(
    private val url: String,
    private val apiKey: String,
) {
    private val http: HttpClient = HttpClient.newBuilder()
        .connectTimeout(Duration.ofSeconds(10))
        .build()

    fun isConfigured(): Boolean = url.isNotEmpty() && apiKey.isNotEmpty()

    /**
     * complete proxies a chat completion through Lens. Throws
     * RuntimeException on non-2xx — action handlers catch and
     * surface a user-visible dialog.
     */
    fun complete(
        messages: List<Map<String, String>>,
        model: String,
        feature: String,
        workspaceId: String,
        issueId: String,
    ): String {
        if (!isConfigured()) {
            throw IllegalStateException("Talyvor Lens is not configured.")
        }
        val body = JSONObject().apply {
            put("model", model)
            put("max_tokens", 2048)
            put("messages", JSONArray(messages.map { msg ->
                JSONObject().apply {
                    put("role", msg["role"])
                    put("content", msg["content"])
                }
            }))
        }
        val request = HttpRequest.newBuilder()
            .uri(URI.create("${url.trimEnd('/')}/v1/proxy/anthropic/v1/messages"))
            .header("Authorization", "Bearer $apiKey")
            .header("Content-Type", "application/json")
            .header("X-Talyvor-Feature", "code-$feature")
            .header("X-Talyvor-Workspace", workspaceId)
            .header("X-Talyvor-Issue", issueId)
            .timeout(Duration.ofSeconds(60))
            .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
            .build()

        val response = http.send(request, HttpResponse.BodyHandlers.ofString())
        if (response.statusCode() >= 400) {
            throw RuntimeException("Lens ${response.statusCode()}: ${response.body()}")
        }
        val json = JSONObject(response.body())
        val content = json.optJSONArray("content") ?: return ""
        val builder = StringBuilder()
        for (i in 0 until content.length()) {
            val entry = content.optJSONObject(i) ?: continue
            if (entry.optString("type") == "text") {
                builder.append(entry.optString("text"))
            }
        }
        return builder.toString()
    }

    /**
     * completeStream proxies a streaming completion. Each text delta
     * fires [StreamCallbacks.onChunk]; the terminal events fire
     * [StreamCallbacks.onDone] with the accumulated token usage; any
     * failure fires [StreamCallbacks.onError]. When Lens ignores
     * `stream:true` and returns plain JSON (some proxies do), the body
     * is read once and emitted as a single chunk + done so callers
     * never have to fall back manually. Endpoint + request shape are
     * chosen per provider, mirroring the VS Code LensClient.completeStream
     * and the Go agent's SSE readers.
     *
     * Runs synchronously on the calling thread; callers invoke it from a
     * background thread and marshal the callbacks onto the EDT.
     */
    fun completeStream(
        messages: List<Map<String, String>>,
        model: String,
        feature: String,
        workspaceId: String,
        issueId: String,
        callbacks: StreamCallbacks,
    ) {
        if (!isConfigured()) {
            callbacks.onError(IllegalStateException("Talyvor Lens is not configured."))
            return
        }
        val provider = SsePure.providerForModel(model)
        val base = url.trimEnd('/')
        val endpoint = if (provider == StreamProvider.OPENAI) {
            "$base/v1/proxy/openai/v1/chat/completions"
        } else {
            "$base/v1/proxy/anthropic/v1/messages"
        }
        val body = JSONObject().apply {
            put("model", model)
            // Anthropic requires max_tokens; the OpenAI chat endpoint
            // does not, and Lens forwards each shape verbatim.
            if (provider == StreamProvider.ANTHROPIC) put("max_tokens", 2048)
            put("messages", JSONArray(messages.map { msg ->
                JSONObject().apply {
                    put("role", msg["role"])
                    put("content", msg["content"])
                }
            }))
            put("stream", true)
        }
        val request = HttpRequest.newBuilder()
            .uri(URI.create(endpoint))
            .header("Authorization", "Bearer $apiKey")
            .header("Content-Type", "application/json")
            .header("Accept", "text/event-stream")
            .header("X-Talyvor-Feature", "code-$feature")
            .header("X-Talyvor-Workspace", workspaceId)
            .header("X-Talyvor-Issue", issueId)
            .timeout(Duration.ofSeconds(120))
            .POST(HttpRequest.BodyPublishers.ofString(body.toString()))
            .build()

        val response = try {
            http.send(request, HttpResponse.BodyHandlers.ofInputStream())
        } catch (e: Exception) {
            callbacks.onError(e)
            return
        }
        val status = response.statusCode()
        if (status >= 400) {
            val errBody = response.body().bufferedReader(Charsets.UTF_8).use { it.readText() }
            callbacks.onError(RuntimeException("Lens $status: $errBody"))
            return
        }
        val contentType = response.headers().firstValue("content-type").orElse("")
        if (!contentType.lowercase().startsWith("text/event-stream")) {
            val jsonBody = response.body().bufferedReader(Charsets.UTF_8).use { it.readText() }
            consumeNonStream(jsonBody, provider, callbacks)
            return
        }

        val buf = SseBuffer()
        var inputTokens = 0
        var outputTokens = 0
        var errored = false
        fun handle(block: String) {
            if (errored) return
            val ev = SsePure.parseEvent(SsePure.extractData(block), provider) ?: return
            when (ev.kind) {
                StreamEventKind.TEXT -> ev.text?.let { callbacks.onChunk(it) }
                StreamEventKind.ERROR -> {
                    errored = true
                    callbacks.onError(RuntimeException(ev.message ?: "stream error"))
                }
                StreamEventKind.DONE -> {
                    if (ev.inputTokens != 0) inputTokens = ev.inputTokens
                    if (ev.outputTokens != 0) outputTokens = ev.outputTokens
                }
            }
        }
        try {
            response.body().bufferedReader(Charsets.UTF_8).use { reader ->
                val chunk = CharArray(4096)
                while (true) {
                    val n = reader.read(chunk)
                    if (n < 0) break
                    for (blk in buf.push(String(chunk, 0, n))) handle(blk)
                    if (errored) return
                }
                for (blk in buf.flush()) handle(blk)
            }
        } catch (e: Exception) {
            if (!errored) callbacks.onError(e)
            return
        }
        if (!errored) callbacks.onDone(inputTokens, outputTokens)
    }

    // consumeNonStream is the JSON fallback path — used when the Lens
    // response Content-Type isn't text/event-stream. Reads the body
    // once and emits it as a single chunk + done. Mirrors the VS Code
    // consumeNonStream.
    private fun consumeNonStream(
        body: String,
        provider: StreamProvider,
        callbacks: StreamCallbacks,
    ) {
        try {
            val obj = JSONObject(body)
            if (provider == StreamProvider.OPENAI) {
                val text = obj.optJSONArray("choices")
                    ?.optJSONObject(0)
                    ?.optJSONObject("message")
                    ?.optString("content", "")
                    .orEmpty()
                if (text.isNotEmpty()) callbacks.onChunk(text)
                val usage = obj.optJSONObject("usage")
                callbacks.onDone(
                    usage?.optInt("prompt_tokens", 0) ?: 0,
                    usage?.optInt("completion_tokens", 0) ?: 0,
                )
            } else {
                val content = obj.optJSONArray("content")
                val sb = StringBuilder()
                if (content != null) {
                    for (i in 0 until content.length()) {
                        val entry = content.optJSONObject(i) ?: continue
                        if (entry.optString("type") == "text") sb.append(entry.optString("text"))
                    }
                }
                if (sb.isNotEmpty()) callbacks.onChunk(sb.toString())
                val usage = obj.optJSONObject("usage")
                callbacks.onDone(
                    usage?.optInt("input_tokens", 0) ?: 0,
                    usage?.optInt("output_tokens", 0) ?: 0,
                )
            }
        } catch (e: Exception) {
            callbacks.onError(e)
        }
    }

    /**
     * getStatus probes /healthz so the "Test Connection" action can give
     * a fast yes/no without paying for a real inference round-trip.
     * Mirrors the VS Code LensClient.getStatus — any failure is reported
     * as unavailable rather than thrown.
     */
    fun getStatus(): LensStatus {
        if (!isConfigured()) return LensStatus(available = false, version = "unknown")
        return try {
            val request = HttpRequest.newBuilder()
                .uri(URI.create("${url.trimEnd('/')}/healthz"))
                .timeout(Duration.ofSeconds(10))
                .GET()
                .build()
            val response = http.send(request, HttpResponse.BodyHandlers.ofString())
            if (response.statusCode() >= 400) {
                LensStatus(available = false, version = "unknown")
            } else {
                val version = try {
                    JSONObject(response.body()).optString("version", "unknown")
                } catch (_: Exception) {
                    "unknown"
                }
                LensStatus(available = true, version = version)
            }
        } catch (_: Exception) {
            LensStatus(available = false, version = "unknown")
        }
    }
}

/** LensStatus is the /healthz probe result surfaced by Test Connection. */
data class LensStatus(val available: Boolean, val version: String)

/**
 * StreamCallbacks bundles the three streaming sinks, mirroring the VS
 * Code StreamCallbacks interface. onDone carries the accumulated token
 * usage so callers can attribute cost without a second round-trip.
 */
class StreamCallbacks(
    val onChunk: (text: String) -> Unit,
    val onDone: (inputTokens: Int, outputTokens: Int) -> Unit,
    val onError: (error: Throwable) -> Unit,
)
