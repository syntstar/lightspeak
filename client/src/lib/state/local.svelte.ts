// Local self state (mute/deafen, auto-away). The voice engine (phase 4)
// reads these and reports them to the server via voice_state_set.
class LocalState {
  muted = $state(false);
  deafened = $state(false);
  /** Status was switched to "idle" automatically (GetLastInputInfo). */
  autoIdle = $state(false);

  toggleMute(): void {
    if (this.deafened) {
      // Unmuting while deafened undeafens too (Discord behaviour).
      this.deafened = false;
      this.muted = false;
      return;
    }
    this.muted = !this.muted;
  }

  toggleDeafen(): void {
    this.deafened = !this.deafened;
    if (this.deafened) this.muted = true;
    else this.muted = false;
  }
}

export const local = new LocalState();
