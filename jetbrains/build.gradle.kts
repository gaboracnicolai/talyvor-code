// Talyvor Code JetBrains plugin — Phase 1 scaffold.
//
// Targets IntelliJ Community 2024.1 as the floor (sinceBuild =
// "241") and runs on the IntelliJ Platform Gradle Plugin 2.x,
// which moved repository config + SDK declaration into the new
// `intellijPlatform { … }` block.

import org.gradle.api.tasks.PathSensitivity
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

    // testdata/safeurl-cases.json is an INPUT of the test task, and saying so is load-bearing.
    //
    // SafeUrlParityTest asserts this plugin's URL rule against that shared file — the same file the
    // Go and TypeScript ports are asserted against — and the file's own header promises "editing
    // this file alone fails all three". MEASURED, and it did not: the file lives outside this
    // Gradle project, so nothing declared it an input. With this block removed, a green run first
    // (`> Task :test`), a second run with NOTHING changed to prove the up-to-date state exists
    // (`> Task :test UP-TO-DATE`), then the table truncated 29 -> 5 and ONLY the table:
    // `> Task :test UP-TO-DATE` again, exit 0, BUILD SUCCESSFUL, zero tests executed, the guard's
    // own floor never reached. With this block present the same truncation reds on that floor.
    //
    // UP-TO-DATE is the mechanism when the build directory is warm; `org.gradle.caching=true` in
    // gradle.properties means FROM-CACHE is the other spelling of the same hole after a clean.
    // Both were observed. A shared population that one consumer cannot see change is not shared.
    //
    // NAME_ONLY rather than RELATIVE because the file sits above the project directory; what must
    // invalidate the cache is its CONTENT, not where the checkout happens to be rooted.
    test {
        inputs.file(file("../testdata/safeurl-cases.json"))
            .withPropertyName("safeurlSharedCases")
            .withPathSensitivity(PathSensitivity.NAME_ONLY)
    }
}
