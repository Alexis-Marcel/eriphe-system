import { relay } from "./relay.js";

// Back from Google: on to the workspace, or say why not. Nothing is stored or sent elsewhere.
const to = relay(location.href);
if (to) {
  location.replace(to);
} else {
  const fr = (navigator.language || "").toLowerCase().startsWith("fr");
  document.documentElement.lang = fr ? "fr" : "en";
  document.getElementById("msg").textContent = fr
    ? "Ce retour d'autorisation n'est pas valable : relance la connexion depuis ton espace Eriphe."
    : "This authorisation return is not valid: start again from your Eriphe.";
}
