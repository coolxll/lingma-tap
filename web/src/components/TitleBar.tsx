import { Shield, ShieldOff, Pause, Play, Trash2, Sun, Moon } from 'lucide-react';

interface TitleBarProps {
  proxyRunning: boolean;
  isPaused: boolean;
  liveTail: boolean;
  theme: 'dark' | 'light';
  onToggleProxy: () => void;
  onTogglePause: () => void;
  onToggleLiveTail: () => void;
  onClear: () => void;
  onToggleTheme: () => void;
}

export function TitleBar({
  proxyRunning,
  isPaused,
  liveTail,
  theme,
  onToggleProxy,
  onTogglePause,
  onToggleLiveTail,
  onClear,
  onToggleTheme,
}: TitleBarProps) {
  return (
    <div className="flex items-center px-3 pt-7 pb-1 gap-1 border-b border-zinc-800 shrink-0 bg-zinc-950">
      {/* Spacer for macOS traffic lights */}
      <div className="w-16 shrink-0" />

      {/* Title */}
      <div className="flex items-center gap-2 mr-4">
        <span className="text-sm font-bold tracking-tight text-zinc-100">Lingma Tap</span>
      </div>

      {/* Proxy toggle */}
      <button
        onClick={onToggleProxy}
        className={`flex items-center gap-1.5 px-2.5 py-1 rounded text-xs font-medium transition-colors ${
          proxyRunning
            ? 'bg-green-900/50 text-green-400 hover:bg-green-900/70'
            : 'bg-zinc-800 text-zinc-400 hover:bg-zinc-700'
        }`}
        title={proxyRunning ? 'Stop Proxy' : 'Start Proxy'}
      >
        {proxyRunning ? <Shield className="w-3.5 h-3.5" /> : <ShieldOff className="w-3.5 h-3.5" />}
        {proxyRunning ? 'Proxy ON' : 'Proxy OFF'}
      </button>

      <div className="w-px h-5 bg-zinc-800 mx-1" />

      {/* Pause/Resume */}
      <button
        onClick={onTogglePause}
        className={`p-1.5 rounded transition-colors ${
          isPaused ? 'bg-yellow-900/50 text-yellow-400' : 'text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200'
        }`}
        title={isPaused ? 'Resume' : 'Pause'}
      >
        {isPaused ? <Play className="w-3.5 h-3.5" /> : <Pause className="w-3.5 h-3.5" />}
      </button>

      {/* Live Tail */}
      <button
        onClick={onToggleLiveTail}
        className={`px-2 py-1 rounded text-xs transition-colors ${
          liveTail ? 'bg-blue-900/50 text-blue-400' : 'text-zinc-500 hover:bg-zinc-800'
        }`}
        title={liveTail ? 'Disable auto-scroll' : 'Enable auto-scroll'}
      >
        Tail
      </button>

      {/* Clear */}
      <button
        onClick={onClear}
        className="p-1.5 rounded text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
        title="Clear records"
      >
        <Trash2 className="w-3.5 h-3.5" />
      </button>

      <div className="flex-1" />

      {/* Theme */}
      <button
        onClick={onToggleTheme}
        className="p-1.5 rounded text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200 transition-colors"
        title="Toggle theme"
      >
        {theme === 'dark' ? <Sun className="w-3.5 h-3.5" /> : <Moon className="w-3.5 h-3.5" />}
      </button>
    </div>
  );
}
