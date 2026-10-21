(function () {
  var mp = null;
  var fields = null;
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

  function paymentTypeInput() {
    return document.querySelector('input[name="payment_type"]');
  }

  function setPaymentMethod(pm) {
    var idInput = paymentMethodInput();
    var typeInput = paymentTypeInput();
    if (idInput) {
      idInput.value = (pm && pm.id) || "";
    }
    if (typeInput) {
      typeInput.value = (pm && pm.payment_type_id) || "";
    }
  }

  function methodList(res) {
    if (!res) {
      return [];
    }
    if (Array.isArray(res)) {
      return res;
    }
    if (Array.isArray(res.results)) {
      return res.results;
    }
    return [];
  }

  function cardMethod(list) {
    var types = { credit_card: true, debit_card: true, prepaid_card: true };
    for (var i = 0; i < list.length; i++) {
      var pm = list[i];
      if (pm && types[pm.payment_type_id]) {
        return pm;
      }
    }
    return null;
  }

  var lastBin = "";
  var lookupGen = 0;

  function lookupMethod(sdk, bin) {
    var gen = ++lookupGen;
    if (!bin || !sdk || typeof sdk.getPaymentMethods !== "function") {
      setPaymentMethod(null);
      return Promise.resolve();
    }
    return sdk
      .getPaymentMethods({ bin: bin })
      .then(function (res) {
        if (gen !== lookupGen) {
          return;
        }
        setPaymentMethod(cardMethod(methodList(res)));
      })
      .catch(function () {
        if (gen !== lookupGen) {
          return;
        }
        setPaymentMethod(null);
      });
  }

  function secureHosts() {
    return {
      cardNumber: document.getElementById("cardNumber"),
      expirationDate: document.getElementById("expirationDate"),
      securityCode: document.getElementById("securityCode"),
    };
  }

  function hostsReady(hosts) {
    return !!(hosts.cardNumber && hosts.expirationDate && hosts.securityCode);
  }

  function hostsMounted(hosts) {
    return !!(
      hosts.cardNumber.querySelector("iframe") &&
      hosts.expirationDate.querySelector("iframe") &&
      hosts.securityCode.querySelector("iframe")
    );
  }

  function unmountField(field) {
    if (!field || typeof field.unmount !== "function") {
      return;
    }
    try {
      field.unmount();
    } catch (err) {}
  }

  function unmountFields() {
    if (!fields) {
      return;
    }
    unmountField(fields.cardNumber);
    unmountField(fields.expirationDate);
    unmountField(fields.securityCode);
  }

  function swapRemovesSecureFields(event) {
    var tasks = event.detail && event.detail.tasks;
    if (!tasks) {
      return false;
    }
    for (var i = 0; i < tasks.length; i++) {
      var target = tasks[i] && tasks[i].target;
      if (target && typeof target.querySelector === "function" && target.querySelector("#cardNumber")) {
        return true;
      }
    }
    return false;
  }

  function createFields(sdk) {
    var cardNumber = sdk.fields.create("cardNumber", { placeholder: "Número de tarjeta" });
    if (typeof cardNumber.on === "function") {
      cardNumber.on("binChange", function (data) {
        var bin = data && data.bin;
        lastBin = bin || "";
        setPaymentMethod(null);
        lookupMethod(sdk, lastBin);
      });
    }
    fields = {
      cardNumber: cardNumber,
      expirationDate: sdk.fields.create("expirationDate", { placeholder: "MM/AA" }),
      securityCode: sdk.fields.create("securityCode", { placeholder: "Código de seguridad" }),
    };
  }

  function mountFields() {
    var hosts = secureHosts();
    if (!hostsReady(hosts) || hostsMounted(hosts)) {
      return;
    }
    var sdk = client();
    if (!sdk) {
      return;
    }
    if (!fields) {
      createFields(sdk);
    } else {
      unmountFields();
    }
    fields.cardNumber.mount("cardNumber");
    fields.expirationDate.mount("expirationDate");
    fields.securityCode.mount("securityCode");
  }

  function tokenInput(form) {
    return form.querySelector('input[name="token"]');
  }

  function requestBody(ctx) {
    var body = ctx && ctx.request && ctx.request.body;
    if (body && typeof body.set === "function") {
      return body;
    }
    return null;
  }

  function applyToken(form, ctx, id) {
    var input = tokenInput(form);
    if (input) {
      input.value = id;
    }
    var body = requestBody(ctx);
    if (body) {
      body.set("token", id);
    }
  }

  function applyPaymentMethod(ctx) {
    var body = requestBody(ctx);
    if (!body) {
      return;
    }
    var idInput = paymentMethodInput();
    var typeInput = paymentTypeInput();
    body.set("payment_method_id", idInput ? idInput.value : "");
    body.set("payment_type", typeInput ? typeInput.value : "");
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
    var idInput = paymentMethodInput();
    var pending = idInput && idInput.value ? Promise.resolve() : lookupMethod(sdk, lastBin);
    tokenizing = true;
    pending
      .then(function () {
        return sdk.fields.createCardToken({ cardholderName: cardholderName });
      })
      .then(function (result) {
        applyToken(form, event.detail.ctx, result.id);
        applyPaymentMethod(event.detail.ctx);
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

  document.addEventListener("htmx:beforeSwap", function (event) {
    if (swapRemovesSecureFields(event)) {
      unmountFields();
    }
  });
  document.addEventListener("htmx:before:swap", function (event) {
    if (swapRemovesSecureFields(event)) {
      unmountFields();
    }
  });
  document.addEventListener("DOMContentLoaded", mountFields);
  document.addEventListener("htmx:afterSwap", mountFields);
  document.addEventListener("htmx:after:swap", mountFields);
  mountFields();
})();
