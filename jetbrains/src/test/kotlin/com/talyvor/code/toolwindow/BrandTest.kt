// The plugin carries the Talyvor brand (B29.20): the flat mark as its Marketplace and Plugins
// icon in both themes, and the brand colours in the chat tool window.
//
// Touches no IntelliJ Platform API, so it runs under Gradle's default `test` task.

package com.talyvor.code.toolwindow

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.awt.Color

class BrandTest {

    private fun resource(path: String): String {
        val stream = javaClass.getResourceAsStream(path)
        assertNotNull("$path is not in the plugin", stream)
        return stream!!.bufferedReader().readText()
    }

    @Test
    fun pluginIconsAreTheFlatMarkAt40pxInBothThemes() {
        // pluginIcon.svg is the light-theme icon (Obsidian mark), pluginIcon_dark.svg the dark one (Frost).
        for ((path, fill) in listOf(
            "/META-INF/pluginIcon.svg" to "#060A12",
            "/META-INF/pluginIcon_dark.svg" to "#E6EEF7",
        )) {
            val svg = resource(path)
            val root = svg.substringBefore(">")
            assertTrue("$path is not 40×40: $root", root.contains("width=\"40\"") && root.contains("height=\"40\""))
            assertTrue("$path is not the Talyvor mark", svg.contains("aria-label=\"Talyvor mark\""))
            assertTrue("$path is not the $fill mark", svg.contains("fill=\"$fill\""))
        }
    }

    @Test
    fun chatWindowPaintsWithTheBrandOnTheDarkTheme() {
        assertEquals(Color(0x3AD6C0), Brand.accent.dark)
        assertEquals(Color(0xE6EEF7), Brand.ink.dark)
        assertEquals(Color(0x081220), Brand.surface.dark)
    }
}
