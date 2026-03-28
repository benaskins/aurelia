import SwiftUI

enum LaminaTheme {
    // Backgrounds
    static let bg = Color(red: 0.047, green: 0.047, blue: 0.047)          // #0c0c0c
    static let panelBg = Color.white.opacity(0.03)                         // rgba(255,255,255,0.03)
    static let panelBgHover = Color.white.opacity(0.06)
    static let inputBg = Color.white.opacity(0.05)

    // Text
    static let fg = Color(red: 0.91, green: 0.91, blue: 0.91)             // #e8e8e8
    static let muted = Color(red: 0.467, green: 0.467, blue: 0.467)       // #777
    static let dim = Color(red: 0.267, green: 0.267, blue: 0.267)         // #444

    // Accent (rust red)
    static let accent = Color(red: 0.878, green: 0.251, blue: 0.125)      // #e04020
    static let accentDim = Color(red: 0.502, green: 0.125, blue: 0.063)   // #802010

    // Borders
    static let border = Color.white.opacity(0.08)                          // rgba(255,255,255,0.08)

    // Status colors — lamina-compatible
    static let statusOk = Color(red: 0.38, green: 0.79, blue: 0.54)       // Soft green
    static let statusWarn = Color(red: 0.89, green: 0.72, blue: 0.30)     // Warm amber
    static let statusError = accent                                         // Rust red
    static let statusOff = dim                                              // Dim gray

    // Fonts
    static let heading = Font.system(.title3, design: .default, weight: .black)
    static let label = Font.system(.caption, design: .monospaced, weight: .bold)
    static let mono = Font.system(.body, design: .monospaced, weight: .medium)
    static let monoSmall = Font.system(.caption, design: .monospaced)
    static let monoTiny = Font.system(.caption2, design: .monospaced)
    static let body = Font.system(.callout, design: .default, weight: .light)
}
