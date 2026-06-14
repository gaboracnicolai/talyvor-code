// Talyvor Code JetBrains plugin — Phase 1 scaffold.
//
// Targets IntelliJ Community 2024.1 as the floor (sinceBuild =
// "241") and runs on the IntelliJ Platform Gradle Plugin 2.x,
// which moved repository config + SDK declaration into the new
// `intellijPlatform { … }` block.

import org.jetbrains.intellij.platform.gradle.tasks.PatchPluginXmlTask

plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "1.9.22"
    id("org.jetbrains.intellij.platform") version "2.0.1"
}

group = "com.talyvor"
version = "0.1.0"

kotlin {
    jvmToolchain(17)
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    // The IntelliJ Community 2024.1 SDK supplies all the editor +
    // tool window APIs the plugin uses. `instrumentationTools`
    // pulls in the bytecode rewriter Java forms require.
    intellijPlatform {
        intellijIdeaCommunity("2024.1")
        instrumentationTools()
        // pluginVerifier backs the optional `verifyPlugin` task. It
        // is inert for `buildPlugin`; folded in here from a separate
        // trailing block so the verifier resolves when invoked.
        pluginVerifier()
    }
    // org.json is a stable, dep-free JSON library — keeps the
    // plugin classpath small without pulling Jackson.
    implementation("org.json:json:20240303")

    // JUnit 4 backs the pure-logic unit tests (model catalogue, SSE
    // parser, …). Matches the IntelliJ Platform's bundled JUnit and
    // runs under Gradle's default `test` task — no extra config.
    testImplementation("junit:junit:4.13.2")
}

tasks {
    withType<PatchPluginXmlTask> {
        sinceBuild.set("241")
        untilBuild.set("251.*")
    }
}
