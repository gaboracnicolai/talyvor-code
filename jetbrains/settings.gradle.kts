// Repositories live here (not build.gradle.kts) per Gradle 8+
// convention — the platform plugin needs custom Maven repos for
// the IntelliJ Platform distribution.

pluginManagement {
    repositories {
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositories {
        mavenCentral()
        // IntelliJ Platform 2.x relies on the standard JetBrains
        // releases repo for the SDK + bundled plugins.
        maven("https://www.jetbrains.com/intellij-repository/releases")
        maven("https://cache-redirector.jetbrains.com/intellij-dependencies")
    }
}

rootProject.name = "talyvor-code"
