// ==========================================================================
// CloudPool Adaptive Responsive Engine
// Tự động nhận diện thiết bị, độ phân giải màn hình và tối ưu hóa giao diện
// ==========================================================================

const Responsive = {
  currentDevice: 'desktop',
  currentResolution: 'fhd',
  isTouch: false,
  orientation: 'landscape',

  init() {
    this.detectCapabilities();
    this.updateLayout();
    this.bindEvents();
  },

  detectCapabilities() {
    this.isTouch = ('ontouchstart' in window) || (navigator.maxTouchPoints > 0) || (window.matchMedia && window.matchMedia('(pointer: coarse)').matches);
  },

  updateLayout() {
    const width = window.innerWidth;
    const height = window.innerHeight;
    const dpr = window.devicePixelRatio || 1;

    // Detect Device Category
    if (width < 650) {
      this.currentDevice = 'mobile';
    } else if (width < 1024) {
      this.currentDevice = 'tablet';
    } else if (width < 1440) {
      this.currentDevice = 'laptop';
    } else if (width < 2000) {
      this.currentDevice = 'desktop';
    } else {
      this.currentDevice = 'ultrawide'; // 2K, 4K, Ultrawide displays
    }

    // Detect Resolution Category
    if (width >= 2560) {
      this.currentResolution = '4k';
    } else if (width >= 1920) {
      this.currentResolution = '2k';
    } else if (width >= 1280) {
      this.currentResolution = 'fhd';
    } else if (width >= 768) {
      this.currentResolution = 'hd';
    } else {
      this.currentResolution = 'mobile';
    }

    // Detect Orientation
    this.orientation = width > height ? 'landscape' : 'portrait';

    // Apply attributes to <html> & <body>
    const root = document.documentElement;
    root.setAttribute('data-device', this.currentDevice);
    root.setAttribute('data-resolution', this.currentResolution);
    root.setAttribute('data-touch', this.isTouch ? 'true' : 'false');
    root.setAttribute('data-orientation', this.orientation);
    root.setAttribute('data-dpr', dpr >= 2 ? 'retina' : 'standard');

    // Notify other components
    window.dispatchEvent(new CustomEvent('cloudpool:resize', {
      detail: {
        device: this.currentDevice,
        resolution: this.currentResolution,
        width,
        height,
        isTouch: this.isTouch
      }
    }));
  },

  bindEvents() {
    let resizeTimer = null;
    window.addEventListener('resize', () => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        this.updateLayout();
      }, 50);
    });

    window.addEventListener('orientationchange', () => {
      setTimeout(() => this.updateLayout(), 150);
    });
  },

  isMobile() { return this.currentDevice === 'mobile'; },
  isTablet() { return this.currentDevice === 'tablet'; },
  isLaptop() { return this.currentDevice === 'laptop'; },
  isDesktop() { return this.currentDevice === 'desktop'; },
  isUltrawide() { return this.currentDevice === 'ultrawide'; }
};

// Auto-initialize immediately
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => Responsive.init());
} else {
  Responsive.init();
}
