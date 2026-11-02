package main

// The words of Settings → Folders (web_folders.go, templates/folders.html).

func init() {
	for lang, words := range folderStrings {
		for k, v := range words {
			webStrings[lang][k] = v
		}
	}
}

var folderStrings = map[string]map[string]string{
	"fr": {
		"settings.folders":        "Dossiers",
		"folders.lead":            "Les dossiers de tes appareils que tu ouvres à {{name}} : il ne voit rien d'autre, et ne modifie ni ne supprime jamais tes fichiers. Un dossier se partage sur l'appareil qui le contient, jamais d'ici : ici, tu vois ce qui est partagé et tu peux arrêter de partager.",
		"folders.drive":           "Dans ton Google Drive",
		"folders.drivelend":       "Lu par les tâches à qui {{name}} prête l'outil Google Drive.",
		"folders.adddrive":        "Donner un dossier de Google Drive…",
		"folders.none":            "Aucun dossier partagé sur cet appareil.",
		"folders.nomachine":       "Aucun appareil pour l'instant.",
		"folders.delete":          "Ne plus partager",
		"folders.deleteconfirm":   "Ne plus partager « %s » avec {{name}} ? Les tâches qui l'utilisent s'arrêtent, tes fichiers restent où ils sont.",
		"folders.mount":           "Lu par les tâches dans %s",
		"folders.missing":         "absent pour l'instant",
		"folders.access.read":     "Lecture",
		"folders.access.droplead": "{{name}} lit les fichiers et dépose ses résultats dans le sous-dossier « {{name}} », créé si besoin. Tes originaux ne sont jamais modifiés.",
		"folders.access.drop":     "Lecture et dépôt des résultats",
		"folders.share":           "Partager un dossier de cet appareil",
		"folders.how.mac":         "Sur ce Mac, dans le Terminal : « ~/.eriphe/bin/eriphe-share add ~/Documents/Clients » (lecture seule) ; ajoute --drop pour que {{name}} dépose aussi ses résultats dans son sous-dossier. « eriphe-share list » montre ce qui est partagé, « eriphe-share remove Clients » arrête.",
		"folders.how.linux":       "Sur cet appareil, dans un terminal : « sudo eriphe-share add /chemin/du/dossier » (lecture seule) ; ajoute --drop pour que {{name}} dépose aussi ses résultats dans son sous-dossier. « sudo eriphe-share list » montre ce qui est partagé, « sudo eriphe-share remove NOM » arrête. Seuls les emplacements prévus à l'installation (--share) sont possibles.",
		"folders.how.docker":      "Sur cet appareil (le NAS), dans un terminal : « docker exec eriphe-node eriphe-node share add /chemin/du/dossier » (lecture seule) ; ajoute --drop pour que {{name}} dépose aussi ses résultats dans son sous-dossier. « … share list » montre ce qui est partagé, « … share remove NOM » arrête. Seuls les emplacements prévus à l'installation (ERIPHE_SHARES) sont possibles.",
		"folders.how.windows":     "Le partage de dossiers n'est pas encore possible sur Windows.",
		"folders.how.lima":        "Cet appareil (Mac avec la VM Lima) ne voit aucun dossier du Mac : réinstalle-le avec le container d'Apple pour partager des dossiers.",
		"folders.how.node":        "Sur cet appareil : « eriphe-node share add /chemin/du/dossier » (lecture seule, --drop pour le dépôt des résultats), avec le même utilisateur et les mêmes réglages que le nœud.",
		"folders.legacylead":      "Le partage se fait maintenant sur l'appareil qui contient le dossier. Les dossiers donnés depuis cette page avant ne sont plus prêtés aux tâches : partage-les de nouveau sur leur appareil (voir chaque appareil ci-dessous), puis oublie l'ancienne entrée.",
		"folders.legacy":          "Donnés depuis cette page avant, plus prêtés :",
		"folders.reshare":         "À partager de nouveau sur l'appareil",
		"folders.forget":          "Oublier",
		"folders.err.gone":        "Ce dossier n'est déjà plus partagé.",
	},
	"en": {
		"settings.folders":        "Folders",
		"folders.lead":            "The folders of your devices you open to {{name}}: it sees nothing else, and never changes or deletes your files. A folder is shared on the device that holds it, never from here: here you see what is shared and can stop sharing it.",
		"folders.drive":           "In your Google Drive",
		"folders.drivelend":       "Read by the tasks {{name}} lends the Google Drive tool.",
		"folders.adddrive":        "Give a Google Drive folder…",
		"folders.none":            "No folder shared on this device.",
		"folders.nomachine":       "No device yet.",
		"folders.delete":          "Stop sharing",
		"folders.deleteconfirm":   "Stop sharing \"%s\" with {{name}}? The tasks using it stop; your files stay where they are.",
		"folders.mount":           "Read by tasks at %s",
		"folders.missing":         "not there now",
		"folders.access.read":     "Read",
		"folders.access.droplead": "{{name}} reads the files and drops its results in the \"{{name}}\" subfolder, created if needed. Your originals are never changed.",
		"folders.access.drop":     "Read and drop results",
		"folders.share":           "Share a folder of this device",
		"folders.how.mac":         "On this Mac, in the Terminal: \"~/.eriphe/bin/eriphe-share add ~/Documents/Clients\" (read-only); add --drop to let {{name}} also drop its results in its subfolder. \"eriphe-share list\" shows what is shared, \"eriphe-share remove Clients\" stops it.",
		"folders.how.linux":       "On this device, in a terminal: \"sudo eriphe-share add /path/to/folder\" (read-only); add --drop to let {{name}} also drop its results in its subfolder. \"sudo eriphe-share list\" shows what is shared, \"sudo eriphe-share remove NAME\" stops it. Only the places set at install (--share) are possible.",
		"folders.how.docker":      "On this device (the NAS), in a terminal: \"docker exec eriphe-node eriphe-node share add /path/to/folder\" (read-only); add --drop to let {{name}} also drop its results in its subfolder. \"… share list\" shows what is shared, \"… share remove NAME\" stops it. Only the places set at install (ERIPHE_SHARES) are possible.",
		"folders.how.windows":     "Folders cannot be shared on Windows yet.",
		"folders.how.lima":        "This device (a Mac with the Lima VM) sees no folder of the Mac: install it again with Apple's container to share folders.",
		"folders.how.node":        "On this device: \"eriphe-node share add /path/to/folder\" (read-only, --drop to drop results), as the node's user and with its settings.",
		"folders.legacylead":      "Folders are now shared on the device that holds them. Those given from this page before are no longer lent to tasks: share them again on their device (see each device below), then forget the old entry.",
		"folders.legacy":          "Given from this page before, no longer lent:",
		"folders.reshare":         "To share again on the device",
		"folders.forget":          "Forget",
		"folders.err.gone":        "This folder is no longer shared already.",
	},
}
