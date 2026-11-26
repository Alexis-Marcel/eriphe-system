Écrit le 26/11/2026 à 11 h par la tâche « Corriger le lancement iOS ».

L'app se ferme au lancement sur iOS 19 : une clé manquante dans `Info.plist` fait planter le module de notifications.

> [!steps] Correctif
> - [x] Reproduire le plantage 📅 2026-11-24
>   - Sur simulateur iOS 19.1, à chaque lancement à froid.
> - [x] Trouver la cause 📅 2026-11-25
>   - Le module de notifications lit `NSUserNotificationsUsageDescription`, absente depuis la 3.2.0.
> - [/] Corriger et tester 📅 2026-11-26
>   - Clé ajoutée, tests verts ; reste la vérification sur un vrai téléphone.
> - [ ] Publier en production 📅 2026-11-27
>   - Après ton oui, version 3.2.1.

> [!figures|aside] Vérifications
> - Tests : 44/44
> - Build iOS : vert
> - Taille : 38,2 Mo (▲ 0,1 Mo)

```diff
--- a/ios/App/Info.plist
+++ b/ios/App/Info.plist
@@ -41,6 +41,8 @@
   <key>CFBundleVersion</key>
-  <string>3.2.0</string>
+  <string>3.2.1</string>
+  <key>NSUserNotificationsUsageDescription</key>
+  <string>Pour te prévenir quand une tâche est finie.</string>
```

- [Fix: l'app plante au lancement sur iOS 19][pr1] : revue approuvée
- [Page du ticket](https://example.org/example/app/issues/127)

### Décisions
- Pas de retour à la 3.1 : le correctif est plus rapide que le retour arrière.
- La clé reste obligatoire : un test vérifie sa présence.

### Publier
[Publier en prod](card:calls:530)

[Teste sur un vrai téléphone d'abord](ask:)

[pr1]: pr:example/app#128
