# -*- coding: utf-8 -*-
import io
path = r'f:\supportflast.dev\supportflast_ui\storage\js\preview.js'
with io.open(path, 'r', encoding='utf-8') as f:
    js = f.read()

# 1. Change preload="metadata" to preload="auto"
js = js.replace('preload="metadata"', 'preload="auto" crossorigin="anonymous"')

# 2. Add Smart Buffering and Watchdog reset
smart_buffer_logic = """
        vidPlayer.addEventListener('progress', () => {
          // Reset watchdog on progress
          if (PreviewManager._videoWatchdog) {
            clearTimeout(PreviewManager._videoWatchdog);
          }
          PreviewManager._videoWatchdog = setTimeout(() => {
            if (vidPlayer && vidPlayer.readyState === 0 && !vidPlayer.error) {
              handleVideoFailure();
            }
          }, 15000);
          
          if (vidPlayer.buffered.length > 0 && loadStatus.style.display !== 'none') {
             let cur = vidPlayer.currentTime;
             let bufEnd = vidPlayer.buffered.end(vidPlayer.buffered.length - 1);
             let ahead = bufEnd - cur;
             if (ahead > 1.5) {
                loadStatus.style.display = 'none';
             } else {
                if (loadText) loadText.innerHTML = '⚡ Đang đệm thêm dữ liệu... (' + ahead.toFixed(1) + 's)';
             }
          }
        });
        
        vidPlayer.addEventListener('canplaythrough', () => {
          loadStatus.style.display = 'none';
        });
"""

js = js.replace("vidPlayer.addEventListener('error', handleVideoFailure);", "vidPlayer.addEventListener('error', handleVideoFailure);\n" + smart_buffer_logic)

with io.open(path, 'w', encoding='utf-8') as f:
    f.write(js)
