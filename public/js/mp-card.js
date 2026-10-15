(function () {
  var mounted = false;
  var mp = null;
  var tokenizing = false;

  function publicKey() {
    var el = document.querySelector("[data-mp-key]");
    if (!el) {
      return "";
    }
    return el.getAttribute("data-mp-key") || "";
  }

  function client() {
    if (mp) {
      return mp;
    }
    var key = publicKey();
    if (!key || typeof MercadoPago !== "function") {
      return null;
    }
    mp = new MercadoPago(key, { locale: "es-MX" });
    return mp;
  }

  function paymentMethodInput() {
    return document.querySelector('input[name="payment_method_id"]');
  }

  function setPaymentMethodID(id) {
    var input = paymentMethodInput();
    if (input) {
      input.value = id || "";
    }
  }

  function mountFields() {
    if (mounted) {
      return;
    }
    if (
      !document.getElementById("cardNumber") ||
      !document.getElementById("expirationDate") ||
      !document.getElementById("securityCode")
    ) {
      return;
    }
    var sdk = client();
    if (!sdk) {
      return;
    }
    var cardNumber = sdk.fields.create("cardNumber", { placeholder: "Número de tarjeta" });
    cardNumber.mount("cardNumber");
    if (typeof cardNumber.on === "function") {
      cardNumber.on("binChange", function (data) {
        var bin = data && data.bin;
        if (!bin) {
          setPaymentMethodID("");
          return;
        }
        if (typeof sdk.getPaymentMethods !== "function") {
          return;
        }
        sdk
          .getPaymentMethods({ bin: bin })
          .then(function (res) {
            var id = res && res.results && res.results[0] && res.results[0].id;
            setPaymentMethodID(id || "");
          })
          .catch(function () {
            setPaymentMethodID("");
          });
      });
    }
    sdk.fields.create("expirationDate", { placeholder: "MM/AA" }).mount("expirationDate");
    sdk.fields.create("securityCode", { placeholder: "Código de seguridad" }).mount("securityCode");
    mounted = true;
  }

  function tokenInput(form) {
    return form.querySelector('input[name="token"]');
  }

  function applyToken(form, ctx, id) {
    var input = tokenInput(form);
    if (input) {
      input.value = id;
    }
    var body = ctx && ctx.request && ctx.request.body;
    if (body && typeof body.set === "function") {
      body.set("token", id);
    }
  }

  function clearPayToken() {
    var form = document.getElementById("pay_form");
    if (!form) {
      return;
    }
    var input = tokenInput(form);
    if (input) {
      input.value = "";
    }
  }

  document.addEventListener("htmx:confirm", function (event) {
    var form = event.target;
    if (!form || form.id !== "pay_form") {
      return;
    }
    event.preventDefault();
    if (tokenizing) {
      return;
    }
    var sdk = client();
    if (!sdk) {
      return;
    }
    var nameEl = document.getElementById("card_name");
    var cardholderName = nameEl ? nameEl.value : "";
    tokenizing = true;
    sdk.fields
      .createCardToken({ cardholderName: cardholderName })
      .then(function (result) {
        applyToken(form, event.detail.ctx, result.id);
        event.detail.issueRequest();
      })
      .finally(function () {
        tokenizing = false;
      });
  });

  document.addEventListener("htmx:responseError", function (event) {
    var elt = event.detail && event.detail.elt;
    var target = event.detail && event.detail.target;
    if ((elt && (elt.id === "pay_form" || elt.id === "checkout_panel")) || (target && target.id === "checkout_panel")) {
      clearPayToken();
    }
  });

  document.addEventListener("DOMContentLoaded", mountFields);
  document.addEventListener("htmx:afterSwap", mountFields);
  document.addEventListener("htmx:after:swap", mountFields);
  mountFields();
})();
