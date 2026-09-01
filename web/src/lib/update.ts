export interface UpdateInfo {
  supported: boolean;
  available: boolean;
  current_version: string;
  latest_version: string;
  release_url: string;
  asset_name: string;
}

export type UpdatePhase = 'idle' | 'downloading' | 'verifying' | 'staging' | 'restarting' | 'error';

export interface UpdateProgress {
  phase: UpdatePhase;
  downloaded_bytes?: number;
  total_bytes?: number;
  error?: string;
  manual_required?: boolean;
  release_url?: string;
}
