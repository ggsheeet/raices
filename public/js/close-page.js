document.querySelectorAll("[data-close-page]").forEach((btn) => {
  btn.addEventListener("click", () => {
    window.close();
    window.setTimeout(() => {
      const hint = document.getElementById("close_hint");
      if (hint) {
        hint.hidden = false;
      }
    }, 300);
  });
});
