import AppKit
import SwiftUI

enum MenuBarIcon {
    /// Renders a bold "A" as an NSImage tinted with the given color for the menu bar.
    static func make(color: NSColor) -> NSImage {
        let size = NSSize(width: 18, height: 18)
        let image = NSImage(size: size, flipped: false) { rect in
            let font = NSFont.systemFont(ofSize: 14, weight: .black)
            let attrs: [NSAttributedString.Key: Any] = [
                .font: font,
                .foregroundColor: color,
            ]
            let str = NSAttributedString(string: "A", attributes: attrs)
            let strSize = str.size()
            let origin = NSPoint(
                x: (rect.width - strSize.width) / 2,
                y: (rect.height - strSize.height) / 2
            )
            str.draw(at: origin)
            return true
        }
        image.isTemplate = false
        return image
    }

    static func ok() -> NSImage { make(color: nsColor(LaminaTheme.statusOk)) }
    static func warning() -> NSImage { make(color: nsColor(LaminaTheme.statusWarn)) }
    static func error() -> NSImage { make(color: nsColor(LaminaTheme.accent)) }
    static func disconnected() -> NSImage { make(color: nsColor(LaminaTheme.muted)) }

    private static func nsColor(_ color: Color) -> NSColor {
        NSColor(color)
    }
}
