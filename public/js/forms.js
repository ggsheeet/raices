function setupFormErrorDetection() {
  document.querySelectorAll('[role="alert"][id]').forEach((slot) => {
    const control = document.querySelector('[aria-describedby~="' + CSS.escape(slot.id) + '"]');
    if (!control) {
      return;
    }
    if (slot.textContent.trim() !== "") {
      control.setAttribute("aria-invalid", "true");
    } else {
      control.removeAttribute("aria-invalid");
    }
  });
}

document.addEventListener("htmx:after:settle", setupFormErrorDetection);
document.addEventListener("DOMContentLoaded", setupFormErrorDetection);
