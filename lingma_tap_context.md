# Lingma-Tap Project Context

## Overview
Building a packet visualization and capture tool for Lingma API, using the **Go (Wails) + React** stack, inspired by `cursor-tap`.

## Key Technical Details
1. **Encoding (QoderEncoding)**: 
   - Lingma traffic (when `Encode=1` is in URL) uses a custom Base64-based obfuscation.
   - Logic: Custom alphabet mapping + String rearrangement (splitting string into 3 parts and swapping).
   - Reference: See `qoder_go_port.txt` for the Go implementation.

2. **Data Source**: 
   - Initial work focuses on visualizing `traffic-flow/lingma-chat.har`.
   - Goal is to decode and display the nested JSON payloads found in these HAR records.

3. **Architecture Reference**:
   - `../cursor-tap` provides the UI blueprint (Record List, Detail Panel, JSON Viewer).

## Current Status
- Python implementation of encoding is verified.
- Go port of encoding is ready.
- Next step: Initialize Wails project in `lingma-tap` and build the HAR viewer.
