// LensClient is the tiny HTTP wrapper for the Talyvor Lens
// `/v1/proxy/anthropic/v1/messages` endpoint. Uses the JDK's
// java.net.http.HttpClient — no external dep beyond org.json
// (declared in build.gradle.kts).
//
// Every call carries the X-Talyvor-{Feature,Workspace,Issue}
// headers so Lens can attribute spend to the active Track issue
// — same contract as the VS Code extension and the CLI agent.

package com.talyvor.code

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
}
