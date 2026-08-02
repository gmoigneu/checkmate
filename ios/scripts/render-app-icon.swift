#!/usr/bin/env swift
import AppKit
import Foundation

struct Palette {
  let background: NSColor
  let tile: NSColor
  let alternate: NSColor
}

let outputDirectory = URL(
  fileURLWithPath: CommandLine.arguments.dropFirst().first
    ?? "ios/Checkmate/Checkmate/Assets.xcassets/AppIcon.appiconset")

let variants: [(String, Palette)] = [
  (
    "AppIcon.png",
    Palette(
      background: NSColor(red: 192 / 255, green: 94 / 255, blue: 60 / 255, alpha: 1),
      tile: NSColor(red: 1, green: 253 / 255, blue: 250 / 255, alpha: 1),
      alternate: NSColor(red: 231 / 255, green: 177 / 255, blue: 155 / 255, alpha: 1)
    )
  ),
  (
    "AppIcon-Dark.png",
    Palette(
      background: NSColor(red: 26 / 255, green: 23 / 255, blue: 20 / 255, alpha: 1),
      tile: NSColor(red: 224 / 255, green: 138 / 255, blue: 98 / 255, alpha: 1),
      alternate: NSColor(red: 110 / 255, green: 77 / 255, blue: 61 / 255, alpha: 1)
    )
  ),
  (
    "AppIcon-Tinted.png",
    Palette(
      background: .black,
      tile: .white,
      alternate: NSColor(white: 0.48, alpha: 1)
    )
  ),
]

try FileManager.default.createDirectory(at: outputDirectory, withIntermediateDirectories: true)

for (filename, palette) in variants {
  guard
    let bitmap = NSBitmapImageRep(
      bitmapDataPlanes: nil,
      pixelsWide: 1024,
      pixelsHigh: 1024,
      bitsPerSample: 8,
      samplesPerPixel: 4,
      hasAlpha: true,
      isPlanar: false,
      colorSpaceName: .deviceRGB,
      bytesPerRow: 0,
      bitsPerPixel: 0
    )
  else { fatalError("Could not allocate icon bitmap") }

  NSGraphicsContext.saveGraphicsState()
  NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
  palette.background.setFill()
  NSBezierPath(rect: NSRect(x: 0, y: 0, width: 1024, height: 1024)).fill()

  let tileSize: CGFloat = 150
  let gap: CGFloat = 32
  let startX: CGFloat = 82
  let startY: CGFloat = 135
  for index in 0..<5 {
    let rect = NSRect(
      x: startX + CGFloat(index) * (tileSize + gap),
      y: startY + CGFloat(index) * 150,
      width: tileSize,
      height: tileSize
    )
    (index.isMultiple(of: 2) ? palette.tile : palette.alternate).setFill()
    NSBezierPath(roundedRect: rect, xRadius: 30, yRadius: 30).fill()
  }
  NSGraphicsContext.restoreGraphicsState()

  guard let data = bitmap.representation(using: .png, properties: [:]) else {
    fatalError("Could not encode icon")
  }
  try data.write(to: outputDirectory.appending(path: filename), options: .atomic)
}
