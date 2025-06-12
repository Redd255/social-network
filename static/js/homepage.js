document.addEventListener("DOMContentLoaded", () => {
    // ========== create group Buttons ==========

    document.getElementById("create-group-btn").addEventListener("click", () => {
        document.getElementById("create-group-form").classList.remove("hidden");
    });

    document.getElementById("cancel-group-btn").addEventListener("click", () => {
        document.getElementById("create-group-form").classList.add("hidden");
    });

});
