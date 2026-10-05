// Brand — the Talyvor colours the chat tool window paints with (B29.20).
//
// Values are the tokens in the brand package (tokens/tokens.css), each a light/dark pair so
// the window follows the IDE's theme: ChatToolWindow wraps every pair in a JBColor. Plain
// java.awt here, so BrandTest runs without an IDE.

package com.talyvor.code.toolwindow

import java.awt.Color

internal data class BrandColor(val light: Color, val dark: Color)

internal object Brand {
    val surface = BrandColor(Color(0xFFFFFF), Color(0x081220))
    val raised = BrandColor(Color(0xFFFFFF), Color(0x0E1A2A))
    // line: rgba(6,10,18,.10) light, rgba(126,147,171,.18) dark.
    val line = BrandColor(Color(6, 10, 18, 26), Color(126, 147, 171, 46))
    val ink = BrandColor(Color(0x060A12), Color(0xE6EEF7))
    val inkMuted = BrandColor(Color(0x46586E), Color(0x7E93AB))
    val accent = BrandColor(Color(0x0F7A6C), Color(0x3AD6C0))
    val accentTint = BrandColor(Color(0xC9E6E0), Color(0x0E2B2E))
    val critical = BrandColor(Color(0xBF3B2E), Color(0xF0685C))
}
